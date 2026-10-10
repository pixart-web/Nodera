// Package monitoring runs health checks, stores metric samples, evaluates
// alert rules and manages incidents. Checks go through the MonitoringProvider
// (which enforces the SSRF policy), so a monitor can never be used to probe
// internal infrastructure unless the operator explicitly allows it.
package monitoring

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/platform/logger"
	"github.com/nodera/nodera/internal/providers"
	"github.com/nodera/nodera/internal/rbac"
)

type AuditRecorder interface {
	Record(ctx context.Context, ac authctx.AuthContext, e audit.Entry) error
}

// Dispatcher delivers alert messages (implemented by notifications.Service).
type Dispatcher interface {
	Dispatch(ctx context.Context, orgID uuid.UUID, kinds []string, kind, title, body, resType, resID string) []string
}

type Service struct {
	pool      *pgxpool.Pool
	audit     AuditRecorder
	providers providers.Set
	notify    Dispatcher
}

func New(pool *pgxpool.Pool, a AuditRecorder, set providers.Set, n Dispatcher) *Service {
	return &Service{pool: pool, audit: a, providers: set, notify: n}
}

var (
	hostRe     = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	hostPortRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?:[0-9]{1,5}$`)
	resourceRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	validKinds = map[string]bool{"http": true, "tcp": true, "dns": true, "ssl": true, "container": true, "database": true}
)

type Monitor struct {
	ID              uuid.UUID  `json:"id"`
	ProjectID       *uuid.UUID `json:"project_id"`
	Kind            string     `json:"kind"`
	Name            string     `json:"name"`
	Target          string     `json:"target"`
	IntervalSeconds int        `json:"interval_seconds"`
	Enabled         bool       `json:"enabled"`
	LastStatus      string     `json:"last_status"`
	LastCheckedAt   *time.Time `json:"last_checked_at"`
	LastLatencyMs   *int       `json:"last_latency_ms"`
	LastError       string     `json:"last_error,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

type MonitorInput struct {
	ProjectID       *uuid.UUID `json:"project_id"`
	Kind            string     `json:"kind"`
	Name            string     `json:"name"`
	Target          string     `json:"target"`
	IntervalSeconds int        `json:"interval_seconds"`
	Enabled         *bool      `json:"enabled"`
}

func (in *MonitorInput) validate() error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 100 {
		return apierr.Validation("name is required (max 100 characters)")
	}
	if !validKinds[in.Kind] {
		return apierr.Validation("kind must be http, tcp, dns, ssl, container or database")
	}
	if in.IntervalSeconds == 0 {
		in.IntervalSeconds = 60
	}
	if in.IntervalSeconds < 10 || in.IntervalSeconds > 86400 {
		return apierr.Validation("interval_seconds must be between 10 and 86400")
	}
	in.Target = strings.TrimSpace(in.Target)
	switch in.Kind {
	case "http":
		u, err := url.Parse(in.Target)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
			return apierr.Validation("http target must be an http(s) URL without credentials")
		}
	case "tcp":
		if !hostPortRe.MatchString(in.Target) {
			return apierr.Validation("tcp target must be host:port")
		}
	case "dns", "ssl":
		if !hostRe.MatchString(strings.Split(in.Target, ":")[0]) || len(in.Target) > 260 {
			return apierr.Validation(in.Kind + " target must be a hostname")
		}
	case "container", "database":
		if !resourceRe.MatchString(in.Target) {
			return apierr.Validation(in.Kind + " target must be a container/database name")
		}
	}
	return nil
}

const monCols = `id, project_id, kind, name, target, interval_seconds, enabled, last_status, last_checked_at, last_latency_ms, COALESCE(last_error,''), created_at`

func scanMon(row pgx.Row) (Monitor, error) {
	var m Monitor
	err := row.Scan(&m.ID, &m.ProjectID, &m.Kind, &m.Name, &m.Target, &m.IntervalSeconds, &m.Enabled, &m.LastStatus, &m.LastCheckedAt, &m.LastLatencyMs, &m.LastError, &m.CreatedAt)
	return m, err
}

func (s *Service) rec(ctx context.Context, ac authctx.AuthContext, action, typ, id string, state any) {
	if err := s.audit.Record(ctx, ac, audit.Entry{Action: action, ResourceType: typ, ResourceID: id, Success: true, ResultingState: state}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}
}

func (s *Service) CreateMonitor(ctx context.Context, ac authctx.AuthContext, in MonitorInput) (Monitor, error) {
	if err := rbac.Require(ac, "monitoring.manage"); err != nil {
		return Monitor{}, err
	}
	if err := in.validate(); err != nil {
		return Monitor{}, err
	}
	enabled := in.Enabled == nil || *in.Enabled
	m, err := scanMon(s.pool.QueryRow(ctx, `
		INSERT INTO monitors (organization_id, project_id, kind, name, target, interval_seconds, enabled)
		SELECT $1,$2,$3,$4,$5,$6,$7 WHERE $2::uuid IS NULL OR EXISTS (SELECT 1 FROM projects WHERE id=$2 AND organization_id=$1 AND deleted_at IS NULL)
		RETURNING `+monCols, ac.OrganizationID, in.ProjectID, in.Kind, in.Name, in.Target, in.IntervalSeconds, enabled))
	if errors.Is(err, pgx.ErrNoRows) {
		return Monitor{}, apierr.Validation("project_id does not refer to a project in this organization")
	}
	if err != nil {
		return Monitor{}, apierr.Conflict("a monitor with this name already exists")
	}
	s.rec(ctx, ac, "monitoring.monitor.created", "monitor", m.ID.String(), m)
	return m, nil
}

func (s *Service) ListMonitors(ctx context.Context, ac authctx.AuthContext, projectID *uuid.UUID, limit, offset int) ([]Monitor, error) {
	if err := rbac.Require(ac, "monitoring.read"); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT `+monCols+` FROM monitors WHERE organization_id=$1 AND ($2::uuid IS NULL OR project_id=$2)
		ORDER BY name LIMIT $3 OFFSET $4`, ac.OrganizationID, projectID, limit, offset)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list monitors", err)
	}
	defer rows.Close()
	out := []Monitor{}
	for rows.Next() {
		m, err := scanMon(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Service) UpdateMonitor(ctx context.Context, ac authctx.AuthContext, id uuid.UUID, enabled *bool, interval *int) (Monitor, error) {
	if err := rbac.Require(ac, "monitoring.manage"); err != nil {
		return Monitor{}, err
	}
	if interval != nil && (*interval < 10 || *interval > 86400) {
		return Monitor{}, apierr.Validation("interval_seconds must be between 10 and 86400")
	}
	m, err := scanMon(s.pool.QueryRow(ctx, `UPDATE monitors SET enabled=COALESCE($3,enabled), interval_seconds=COALESCE($4,interval_seconds)
		WHERE id=$1 AND organization_id=$2 RETURNING `+monCols, id, ac.OrganizationID, enabled, interval))
	if errors.Is(err, pgx.ErrNoRows) {
		return Monitor{}, apierr.NotFound("monitor")
	}
	if err != nil {
		return Monitor{}, apierr.Wrap(apierr.CodeInternal, "failed to update monitor", err)
	}
	s.rec(ctx, ac, "monitoring.monitor.updated", "monitor", id.String(), m)
	return m, nil
}

func (s *Service) DeleteMonitor(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) error {
	if err := rbac.Require(ac, "monitoring.manage"); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM monitors WHERE id=$1 AND organization_id=$2`, id, ac.OrganizationID)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to delete monitor", err)
	}
	if tag.RowsAffected() == 0 {
		return apierr.NotFound("monitor")
	}
	s.rec(ctx, ac, "monitoring.monitor.deleted", "monitor", id.String(), nil)
	return nil
}

// CheckResult is what one execution of a monitor produced.
type CheckResult struct {
	Status    string
	LatencyMs int
	Error     string
	Extra     map[string]any
}

func (s *Service) execute(ctx context.Context, m Monitor) CheckResult {
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	need := func() bool { return s.providers.Monitoring != nil }
	fromProvider := func(res providers.CheckResult, err error) CheckResult {
		if err != nil {
			return CheckResult{Status: "failing", Error: err.Error()}
		}
		out := CheckResult{Status: "ok", LatencyMs: res.LatencyMs, Extra: res.Extra}
		if !res.OK {
			out.Status, out.Error = "failing", res.Detail
		}
		return out
	}
	switch m.Kind {
	case "http":
		if !need() {
			return CheckResult{Status: "unknown", Error: "monitoring provider is not configured"}
		}
		return fromProvider(s.providers.Monitoring.CheckHTTP(cctx, m.Target, 0))
	case "tcp":
		if !need() {
			return CheckResult{Status: "unknown", Error: "monitoring provider is not configured"}
		}
		return fromProvider(s.providers.Monitoring.CheckTCP(cctx, m.Target))
	case "dns":
		if !need() {
			return CheckResult{Status: "unknown", Error: "monitoring provider is not configured"}
		}
		return fromProvider(s.providers.Monitoring.CheckDNS(cctx, m.Target))
	case "ssl":
		if !need() {
			return CheckResult{Status: "unknown", Error: "monitoring provider is not configured"}
		}
		res := fromProvider(s.providers.Monitoring.CheckSSL(cctx, m.Target))
		if d, ok := res.Extra["days_remaining"].(float64); ok && res.Status == "ok" && d < 14 {
			res.Status = "warning"
			res.Error = fmt.Sprintf("certificate expires in %.0f days", d)
		}
		return res
	case "container":
		if s.providers.Containers == nil {
			return CheckResult{Status: "unknown", Error: "container provider is not configured"}
		}
		start := time.Now()
		info, ok, err := s.providers.Containers.Inspect(cctx, m.Target)
		switch {
		case err != nil:
			return CheckResult{Status: "failing", Error: err.Error()}
		case !ok:
			return CheckResult{Status: "failing", Error: "container does not exist"}
		case info.State != "running":
			return CheckResult{Status: "failing", Error: "container is " + info.State, LatencyMs: int(time.Since(start).Milliseconds())}
		}
		return CheckResult{Status: "ok", LatencyMs: int(time.Since(start).Milliseconds())}
	case "database":
		if s.providers.DB == nil {
			return CheckResult{Status: "unknown", Error: "database provider is not configured"}
		}
		start := time.Now()
		ok, err := s.providers.DB.Exists(cctx, m.Target)
		if err != nil {
			return CheckResult{Status: "failing", Error: err.Error()}
		}
		if !ok {
			return CheckResult{Status: "failing", Error: "database does not exist"}
		}
		return CheckResult{Status: "ok", LatencyMs: int(time.Since(start).Milliseconds())}
	}
	return CheckResult{Status: "unknown", Error: "unsupported monitor kind"}
}

// RunNow executes one monitor on demand and stores the outcome.
func (s *Service) RunNow(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) (Monitor, error) {
	if err := rbac.Require(ac, "monitoring.manage"); err != nil {
		return Monitor{}, err
	}
	m, err := scanMon(s.pool.QueryRow(ctx, `SELECT `+monCols+` FROM monitors WHERE id=$1 AND organization_id=$2`, id, ac.OrganizationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Monitor{}, apierr.NotFound("monitor")
	}
	if err != nil {
		return Monitor{}, err
	}
	_, _ = s.pool.Exec(ctx, `UPDATE monitors SET last_checked_at=now() WHERE id=$1`, id)
	s.record(ctx, ac.OrganizationID, m, s.execute(ctx, m))
	return scanMon(s.pool.QueryRow(ctx, `SELECT `+monCols+` FROM monitors WHERE id=$1`, id))
}

func (s *Service) record(ctx context.Context, org uuid.UUID, m Monitor, res CheckResult) {
	var lat any
	if res.LatencyMs > 0 || res.Status == "ok" {
		lat = res.LatencyMs
	}
	_, _ = s.pool.Exec(ctx, `UPDATE monitors SET last_status=$2, last_checked_at=COALESCE(last_checked_at, now()), last_latency_ms=$3, last_error=NULLIF($4,'') WHERE id=$1`, m.ID, res.Status, lat, truncate(res.Error, 400))
	if res.Status == "ok" && m.Kind == "http" {
		_, _ = s.pool.Exec(ctx, `INSERT INTO metric_samples (organization_id, project_id, metric, value) VALUES ($1,$2,'http_latency',$3)`, org, m.ProjectID, float64(res.LatencyMs))
	}
	if d, ok := res.Extra["days_remaining"].(float64); ok {
		_, _ = s.pool.Exec(ctx, `INSERT INTO metric_samples (organization_id, project_id, metric, value) VALUES ($1,$2,'ssl_days_remaining',$3)`, org, m.ProjectID, d)
	}
	if m.Kind == "container" {
		v := 0.0
		if res.Status != "ok" {
			v = 1
		}
		_, _ = s.pool.Exec(ctx, `INSERT INTO metric_samples (organization_id, project_id, metric, value) VALUES ($1,$2,'container_unhealthy',$3)`, org, m.ProjectID, v)
	}
	if m.LastStatus != res.Status && m.LastStatus != "unknown" || (m.LastStatus == "unknown" && res.Status == "failing") {
		lvl := "info"
		switch res.Status {
		case "failing":
			lvl = "error"
		case "warning":
			lvl = "warning"
		}
		_, _ = s.pool.Exec(ctx, `INSERT INTO log_entries (organization_id, project_id, source, service, level, message) VALUES ($1,$2,'system','monitoring',$3,$4)`,
			org, m.ProjectID, lvl, fmt.Sprintf("monitor %q changed from %s to %s %s", m.Name, m.LastStatus, res.Status, truncate(res.Error, 200)))
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// RunDue claims and executes every monitor whose interval has elapsed. Claiming
// uses FOR UPDATE SKIP LOCKED so several API instances never double-run one.
func (s *Service) RunDue(ctx context.Context, now time.Time) (int, error) {
	rows, err := s.pool.Query(ctx, `
		WITH due AS (
			SELECT id FROM monitors WHERE enabled AND (last_checked_at IS NULL OR last_checked_at <= $1::timestamptz - make_interval(secs => interval_seconds))
			ORDER BY last_checked_at NULLS FIRST LIMIT 200 FOR UPDATE SKIP LOCKED
		)
		UPDATE monitors m SET last_checked_at = $1::timestamptz FROM due WHERE m.id = due.id
		RETURNING m.id, m.organization_id, m.project_id, m.kind, m.name, m.target, m.interval_seconds, m.enabled, m.last_status, m.last_checked_at, m.last_latency_ms, COALESCE(m.last_error,''), m.created_at`, now)
	if err != nil {
		return 0, err
	}
	type job struct {
		m   Monitor
		org uuid.UUID
	}
	var jobs []job
	for rows.Next() {
		var j job
		m := &j.m
		if err := rows.Scan(&m.ID, &j.org, &m.ProjectID, &m.Kind, &m.Name, &m.Target, &m.IntervalSeconds, &m.Enabled, &m.LastStatus, &m.LastCheckedAt, &m.LastLatencyMs, &m.LastError, &m.CreatedAt); err != nil {
			rows.Close()
			return 0, err
		}
		jobs = append(jobs, j)
	}
	rows.Close()
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(j job) {
			defer wg.Done()
			defer func() { <-sem }()
			s.record(ctx, j.org, j.m, s.execute(ctx, j.m))
		}(j)
	}
	wg.Wait()
	return len(jobs), nil
}

// ---------------- metrics ----------------

type Sample struct {
	Metric    string     `json:"metric"`
	Value     float64    `json:"value"`
	NodeID    *uuid.UUID `json:"node_id,omitempty"`
	ProjectID *uuid.UUID `json:"project_id,omitempty"`
	At        time.Time  `json:"at"`
}

// Metrics returns samples for one metric, newest first.
func (s *Service) Metrics(ctx context.Context, ac authctx.AuthContext, metric string, nodeID, projectID *uuid.UUID, since time.Time, limit int) ([]Sample, error) {
	if err := rbac.Require(ac, "monitoring.read"); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	if since.IsZero() {
		since = time.Now().Add(-24 * time.Hour)
	}
	rows, err := s.pool.Query(ctx, `SELECT metric, value, node_id, project_id, sampled_at FROM metric_samples
		WHERE organization_id=$1 AND metric=$2 AND sampled_at >= $3 AND ($4::uuid IS NULL OR node_id=$4) AND ($5::uuid IS NULL OR project_id=$5)
		ORDER BY sampled_at DESC LIMIT $6`, ac.OrganizationID, metric, since, nodeID, projectID, limit)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to read metrics", err)
	}
	defer rows.Close()
	out := []Sample{}
	for rows.Next() {
		var x Sample
		if err := rows.Scan(&x.Metric, &x.Value, &x.NodeID, &x.ProjectID, &x.At); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
