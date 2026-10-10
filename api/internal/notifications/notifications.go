// Package notifications is the notification center: organisation-wide and
// per-user notifications with per-user read state, plus delivery channels
// (in-app always; webhook with SSRF protection; email only when SMTP is
// configured — until then an email channel records that it cannot deliver
// instead of pretending).
package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/platform/logger"
	"github.com/nodera/nodera/internal/platform/netpolicy"
	"github.com/nodera/nodera/internal/rbac"
)

type AuditRecorder interface {
	Record(ctx context.Context, ac authctx.AuthContext, e audit.Entry) error
}

type Service struct {
	pool   *pgxpool.Pool
	audit  AuditRecorder
	policy netpolicy.Policy
	// SMTPConfigured is false until an SMTP integration exists.
	SMTPConfigured bool
}

func New(pool *pgxpool.Pool, a AuditRecorder, policy netpolicy.Policy) *Service {
	return &Service{pool: pool, audit: a, policy: policy}
}

type Notification struct {
	ID           uuid.UUID `json:"id"`
	Kind         string    `json:"kind"`
	Title        string    `json:"title"`
	Body         string    `json:"body"`
	ResourceType string    `json:"resource_type,omitempty"`
	ResourceID   string    `json:"resource_id,omitempty"`
	Read         bool      `json:"read"`
	CreatedAt    time.Time `json:"created_at"`
}

// Notify satisfies ops.Notifier: it records an organisation-wide notification.
func (s *Service) Notify(ctx context.Context, orgID uuid.UUID, kind, title, body, resourceType, resourceID string) {
	if _, err := s.pool.Exec(ctx, `INSERT INTO notifications (organization_id, kind, title, body, resource_type, resource_id) VALUES ($1,$2,$3,$4,$5,$6)`,
		orgID, kind, truncate(title, 200), truncate(body, 2000), resourceType, resourceID); err != nil {
		logger.FromContext(ctx).Error("failed to store notification", "error", err)
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func userID(ac authctx.AuthContext) (uuid.UUID, error) {
	if ac.ActorType != authctx.ActorUser {
		return uuid.Nil, apierr.Validation("notifications are per-user; use a user session")
	}
	return ac.ActorID, nil
}

// List returns the caller's notifications (org-wide ones plus their own).
func (s *Service) List(ctx context.Context, ac authctx.AuthContext, unreadOnly bool, limit, offset int) ([]Notification, error) {
	uid, err := userID(ac)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT n.id, n.kind, n.title, n.body, n.resource_type, n.resource_id, r.read_at IS NOT NULL, n.created_at
		FROM notifications n LEFT JOIN notification_reads r ON r.notification_id = n.id AND r.user_id = $2
		WHERE n.organization_id = $1 AND (n.user_id IS NULL OR n.user_id = $2) AND (NOT $3 OR r.read_at IS NULL)
		ORDER BY n.created_at DESC, n.id LIMIT $4 OFFSET $5`, ac.OrganizationID, uid, unreadOnly, limit, offset)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list notifications", err)
	}
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.Kind, &n.Title, &n.Body, &n.ResourceType, &n.ResourceID, &n.Read, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Service) UnreadCount(ctx context.Context, ac authctx.AuthContext) (int, error) {
	uid, err := userID(ac)
	if err != nil {
		return 0, err
	}
	var n int
	err = s.pool.QueryRow(ctx, `SELECT count(*) FROM notifications n
		WHERE n.organization_id=$1 AND (n.user_id IS NULL OR n.user_id=$2)
		  AND NOT EXISTS (SELECT 1 FROM notification_reads r WHERE r.notification_id=n.id AND r.user_id=$2)`, ac.OrganizationID, uid).Scan(&n)
	return n, err
}

// MarkRead marks the given notifications read; ids from other organisations
// are silently ignored (they simply do not match).
func (s *Service) MarkRead(ctx context.Context, ac authctx.AuthContext, ids []uuid.UUID) error {
	uid, err := userID(ac)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO notification_reads (notification_id, user_id)
		SELECT n.id, $2 FROM notifications n WHERE n.id = ANY($3) AND n.organization_id=$1 AND (n.user_id IS NULL OR n.user_id=$2)
		ON CONFLICT DO NOTHING`, ac.OrganizationID, uid, ids)
	return err
}

func (s *Service) MarkAllRead(ctx context.Context, ac authctx.AuthContext) error {
	uid, err := userID(ac)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO notification_reads (notification_id, user_id)
		SELECT n.id, $2 FROM notifications n WHERE n.organization_id=$1 AND (n.user_id IS NULL OR n.user_id=$2)
		ON CONFLICT DO NOTHING`, ac.OrganizationID, uid)
	return err
}

// ---------------- channels ----------------

type Channel struct {
	ID      uuid.UUID `json:"id"`
	Kind    string    `json:"kind"`
	Name    string    `json:"name"`
	Target  string    `json:"target"`
	Enabled bool      `json:"enabled"`
}

type ChannelInput struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Target  string `json:"target"`
	Enabled *bool  `json:"enabled"`
}

func (s *Service) validateTarget(ctx context.Context, in ChannelInput) error {
	switch in.Kind {
	case "in_app":
		return nil
	case "email":
		if _, err := mailAddr(in.Target); err != nil {
			return apierr.Validation("target must be an email address")
		}
		return nil
	case "webhook":
		u, err := url.Parse(in.Target)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
			return apierr.Validation("webhook target must be an http(s) URL without credentials")
		}
		if _, err := netpolicy.Resolve(ctx, s.policy, u.Host, defaultPort(u.Scheme)); err != nil {
			return apierr.Validation("webhook target is not allowed: " + err.Error())
		}
		return nil
	}
	return apierr.Validation("kind must be in_app, email or webhook")
}

func defaultPort(scheme string) string {
	if scheme == "http" {
		return "80"
	}
	return "443"
}

func mailAddr(s string) (string, error) {
	if len(s) > 254 || !bytes.ContainsRune([]byte(s), '@') || bytes.ContainsAny([]byte(s), " \r\n<>") {
		return "", errors.New("invalid")
	}
	return s, nil
}

func (s *Service) CreateChannel(ctx context.Context, ac authctx.AuthContext, in ChannelInput) (Channel, error) {
	if err := rbac.Require(ac, "monitoring.manage"); err != nil {
		return Channel{}, err
	}
	if in.Name == "" || len(in.Name) > 100 {
		return Channel{}, apierr.Validation("name is required")
	}
	if err := s.validateTarget(ctx, in); err != nil {
		return Channel{}, err
	}
	enabled := in.Enabled == nil || *in.Enabled
	var c Channel
	err := s.pool.QueryRow(ctx, `INSERT INTO notification_channels (organization_id, kind, name, target, enabled) VALUES ($1,$2,$3,$4,$5)
		RETURNING id, kind, name, target, enabled`, ac.OrganizationID, in.Kind, in.Name, in.Target, enabled).Scan(&c.ID, &c.Kind, &c.Name, &c.Target, &c.Enabled)
	if err != nil {
		return Channel{}, apierr.Conflict("a channel with this name already exists")
	}
	_ = s.audit.Record(ctx, ac, audit.Entry{Action: "notifications.channel.created", ResourceType: "notification_channel", ResourceID: c.ID.String(), Success: true,
		ResultingState: map[string]any{"kind": c.Kind, "name": c.Name}})
	return c, nil
}

func (s *Service) ListChannels(ctx context.Context, ac authctx.AuthContext) ([]Channel, error) {
	if err := rbac.Require(ac, "monitoring.read"); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id, kind, name, target, enabled FROM notification_channels WHERE organization_id=$1 ORDER BY name`, ac.OrganizationID)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list channels", err)
	}
	defer rows.Close()
	out := []Channel{}
	for rows.Next() {
		var c Channel
		if err := rows.Scan(&c.ID, &c.Kind, &c.Name, &c.Target, &c.Enabled); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Service) DeleteChannel(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) error {
	if err := rbac.Require(ac, "monitoring.manage"); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM notification_channels WHERE id=$1 AND organization_id=$2`, id, ac.OrganizationID)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to delete channel", err)
	}
	if tag.RowsAffected() == 0 {
		return apierr.NotFound("channel")
	}
	return nil
}

// Dispatch delivers a message to the named channel kinds ("in_app" is always
// recorded as a notification). Webhook delivery re-resolves the target at send
// time under the SSRF policy and pins the connection to the resolved address.
// It returns one error string per failed delivery (never panics, never blocks
// the caller on a slow endpoint beyond the 5s timeout).
func (s *Service) Dispatch(ctx context.Context, orgID uuid.UUID, kinds []string, kind, title, body, resType, resID string) []string {
	var failures []string
	want := map[string]bool{}
	for _, k := range kinds {
		want[k] = true
	}
	if want["in_app"] || len(kinds) == 0 {
		s.Notify(ctx, orgID, kind, title, body, resType, resID)
	}
	rows, err := s.pool.Query(ctx, `SELECT kind, name, target FROM notification_channels WHERE organization_id=$1 AND enabled AND kind <> 'in_app'`, orgID)
	if err != nil {
		return []string{err.Error()}
	}
	type ch struct{ kind, name, target string }
	var chans []ch
	for rows.Next() {
		var c ch
		if rows.Scan(&c.kind, &c.name, &c.target) == nil && (want[c.kind] || want[c.name]) {
			chans = append(chans, c)
		}
	}
	rows.Close()
	payload, _ := json.Marshal(map[string]any{"kind": kind, "title": title, "body": body, "resource_type": resType, "resource_id": resID, "at": time.Now().UTC()})
	for _, c := range chans {
		switch c.kind {
		case "webhook":
			if err := s.postWebhook(ctx, c.target, payload); err != nil {
				failures = append(failures, fmt.Sprintf("webhook %s: %v", c.name, err))
			}
		case "email":
			if !s.SMTPConfigured {
				failures = append(failures, fmt.Sprintf("email %s: SMTP is not configured; message not sent", c.name))
			}
		}
	}
	return failures
}

func (s *Service) postWebhook(ctx context.Context, target string, payload []byte) error {
	u, err := url.Parse(target)
	if err != nil {
		return err
	}
	rt, err := netpolicy.Resolve(ctx, s.policy, u.Host, defaultPort(u.Scheme))
	if err != nil {
		return err
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, rt.DialAddr())
		}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Nodera-Notifier/1")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("endpoint answered %d", resp.StatusCode)
	}
	return nil
}

var _ = pgx.ErrNoRows
