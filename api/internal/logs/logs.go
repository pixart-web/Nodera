// Package logs implements log management: structured log ingestion with
// secret redaction, filtered/paginated search, live container logs, and the
// data retention sweeper.
package logs

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/platform/redact"
	"github.com/nodera/nodera/internal/projects"
	"github.com/nodera/nodera/internal/providers"
	"github.com/nodera/nodera/internal/rbac"
)

// AuditRecorder is optional: when set, retention changes are audited.
type AuditRecorder interface {
	Record(ctx context.Context, ac authctx.AuthContext, e audit.Entry) error
}

type Service struct {
	pool      *pgxpool.Pool
	providers providers.Set
	audit     AuditRecorder
}

// WithAudit enables auditing of retention-policy changes.
func (s *Service) WithAudit(a AuditRecorder) *Service { s.audit = a; return s }

func New(pool *pgxpool.Pool, set providers.Set) *Service { return &Service{pool: pool, providers: set} }

// Redact removes credentials from a log line before it is stored or returned.
func Redact(s string) string { return redact.String(s) }

var sources = map[string]bool{"application": true, "container": true, "deployment": true, "migration": true, "system": true, "audit": true}
var levels = map[string]bool{"debug": true, "info": true, "success": true, "warning": true, "error": true}

// Ingest stores one log line (redacted, bounded). Invalid source/level values
// are normalised rather than dropped so no line is lost.
func (s *Service) Ingest(ctx context.Context, org uuid.UUID, project *uuid.UUID, source, service, level, message string) error {
	if !sources[source] {
		source = "system"
	}
	if !levels[level] {
		level = "info"
	}
	msg := Redact(message)
	if len(msg) > 8000 {
		msg = msg[:8000] + "…"
	}
	if len(service) > 100 {
		service = service[:100]
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO log_entries (organization_id, project_id, source, service, level, message)
		SELECT $1,$2,$3,$4,$5,$6 WHERE $2::uuid IS NULL OR EXISTS (SELECT 1 FROM projects WHERE id=$2 AND organization_id=$1)`, org, project, source, service, level, msg)
	return err
}

type Entry struct {
	ID        int64      `json:"id"`
	ProjectID *uuid.UUID `json:"project_id"`
	Source    string     `json:"source"`
	Service   string     `json:"service"`
	Level     string     `json:"level"`
	Message   string     `json:"message"`
	At        time.Time  `json:"at"`
}

type Filter struct {
	ProjectID *uuid.UUID
	Source    string
	Level     string
	Query     string
	From, To  time.Time
	Limit     int
	Offset    int
}

func like(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (s *Service) Query(ctx context.Context, ac authctx.AuthContext, f Filter) ([]Entry, error) {
	if err := rbac.Require(ac, "logs.read"); err != nil {
		return nil, err
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT id, project_id, source, service, level, message, created_at FROM log_entries
		WHERE organization_id=$1 AND ($2::uuid IS NULL OR project_id=$2) AND ($3='' OR source=$3) AND ($4='' OR level=$4)
		  AND ($5='' OR message ILIKE '%' || $5 || '%') AND ($6::timestamptz IS NULL OR created_at >= $6) AND ($7::timestamptz IS NULL OR created_at <= $7)
		ORDER BY created_at DESC, id DESC LIMIT $8 OFFSET $9`,
		ac.OrganizationID, f.ProjectID, f.Source, f.Level, like(f.Query), nullTime(f.From), nullTime(f.To), f.Limit, f.Offset)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to query logs", err)
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.Source, &e.Service, &e.Level, &e.Message, &e.At); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// ContainerLogs returns the project's live container output (redacted).
func (s *Service) ContainerLogs(ctx context.Context, ac authctx.AuthContext, projectID uuid.UUID, tail int) ([]string, error) {
	if err := rbac.Require(ac, "logs.read"); err != nil {
		return nil, err
	}
	if s.providers.Containers == nil {
		return nil, apierr.New(apierr.CodeValidation, "container provider is not configured on this deployment")
	}
	var slug string
	if err := s.pool.QueryRow(ctx, `SELECT slug FROM projects WHERE id=$1 AND organization_id=$2 AND deleted_at IS NULL`, projectID, ac.OrganizationID).Scan(&slug); err != nil {
		return nil, apierr.NotFound("project")
	}
	if tail <= 0 || tail > 1000 {
		tail = 200
	}
	lines, err := s.providers.Containers.Logs(ctx, "nodera-"+slug, tail)
	if errors.Is(err, providers.ErrNotFound) {
		return []string{}, nil
	}
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to read container logs", err)
	}
	for i := range lines {
		lines[i] = Redact(lines[i])
	}
	return lines, nil
}

var _ = projects.WorkspacePath

// ---------------- retention ----------------

var defaultRetentionDays = map[string]int{"job_logs": 30, "metric_samples": 30, "log_entries": 30, "incidents": 365}

// audit_log has no default: audit data is only pruned when an organisation
// explicitly sets a retention policy for it.
var resources = map[string]bool{"audit_log": true, "job_logs": true, "metric_samples": true, "incidents": true, "log_entries": true}

type Retention struct {
	Resource      string `json:"resource"`
	RetentionDays int    `json:"retention_days"`
	Default       bool   `json:"default"`
}

func (s *Service) Retention(ctx context.Context, ac authctx.AuthContext) ([]Retention, error) {
	if err := rbac.Require(ac, "monitoring.read"); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT resource, retention_days FROM retention_policies WHERE organization_id=$1`, ac.OrganizationID)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to read retention", err)
	}
	defer rows.Close()
	set := map[string]int{}
	for rows.Next() {
		var r string
		var d int
		if err := rows.Scan(&r, &d); err != nil {
			return nil, err
		}
		set[r] = d
	}
	var out []Retention
	for _, r := range []string{"audit_log", "incidents", "job_logs", "log_entries", "metric_samples"} {
		if d, ok := set[r]; ok {
			out = append(out, Retention{Resource: r, RetentionDays: d})
		} else if d, ok := defaultRetentionDays[r]; ok {
			out = append(out, Retention{Resource: r, RetentionDays: d, Default: true})
		} else {
			out = append(out, Retention{Resource: r, RetentionDays: 0, Default: true}) // 0 = keep forever
		}
	}
	return out, nil
}

func (s *Service) SetRetention(ctx context.Context, ac authctx.AuthContext, resource string, days int) error {
	if err := rbac.Require(ac, "monitoring.manage"); err != nil {
		return err
	}
	if !resources[resource] {
		return apierr.Validation("unknown resource")
	}
	if days < 1 || days > 3650 {
		return apierr.Validation("retention_days must be between 1 and 3650")
	}
	if resource == "audit_log" && days < 90 {
		return apierr.Validation("audit log retention cannot be shorter than 90 days")
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO retention_policies (organization_id, resource, retention_days) VALUES ($1,$2,$3)
		ON CONFLICT (organization_id, resource) DO UPDATE SET retention_days=EXCLUDED.retention_days`, ac.OrganizationID, resource, days)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to save retention", err)
	}
	if s.audit != nil {
		_ = s.audit.Record(ctx, ac, audit.Entry{Action: "logs.retention.set", ResourceType: "retention_policy", ResourceID: resource, Success: true, ResultingState: map[string]any{"retention_days": days}})
	}
	return nil
}

// Sweep deletes data older than each organisation's retention window and
// returns the number of rows removed per resource.
func (s *Service) Sweep(ctx context.Context, now time.Time) (map[string]int64, error) {
	removed := map[string]int64{}
	rows, err := s.pool.Query(ctx, `SELECT id FROM organizations`)
	if err != nil {
		return nil, err
	}
	var orgs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) == nil {
			orgs = append(orgs, id)
		}
	}
	rows.Close()
	for _, org := range orgs {
		days := map[string]int{}
		for k, v := range defaultRetentionDays {
			days[k] = v
		}
		pr, err := s.pool.Query(ctx, `SELECT resource, retention_days FROM retention_policies WHERE organization_id=$1`, org)
		if err != nil {
			return removed, err
		}
		for pr.Next() {
			var r string
			var d int
			if pr.Scan(&r, &d) == nil {
				days[r] = d
			}
		}
		pr.Close()
		cut := func(r string) time.Time { return now.AddDate(0, 0, -days[r]) }
		stmts := []struct{ res, sql string }{
			{"log_entries", `DELETE FROM log_entries WHERE organization_id=$1 AND created_at < $2`},
			{"metric_samples", `DELETE FROM metric_samples WHERE organization_id=$1 AND sampled_at < $2`},
			{"job_logs", `DELETE FROM job_logs WHERE organization_id=$1 AND ts < $2`},
			{"incidents", `DELETE FROM incidents WHERE organization_id=$1 AND status IN ('resolved','closed') AND detected_at < $2`},
			{"audit_log", `DELETE FROM audit_log WHERE organization_id=$1 AND created_at < $2`},
		}
		for _, st := range stmts {
			if _, ok := days[st.res]; !ok {
				continue // no policy and no default: keep
			}
			tag, err := s.pool.Exec(ctx, st.sql, org, cut(st.res))
			if err != nil {
				return removed, err
			}
			removed[st.res] += tag.RowsAffected()
		}
	}
	return removed, nil
}
