// Package dashboard aggregates real platform state for the overview page and
// global search. Every section is permission-gated: a caller only receives
// (and only triggers queries for) data they may read. Nothing is fabricated;
// an empty organisation yields zeros and empty lists.
package dashboard

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/rbac"
)

type Service struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

type RecentOp struct {
	ID        uuid.UUID  `json:"id"`
	Operation string     `json:"operation"`
	Status    string     `json:"status"`
	ProjectID *uuid.UUID `json:"project_id"`
	CreatedAt time.Time  `json:"created_at"`
}

type Summary struct {
	Projects         map[string]int `json:"projects,omitempty"`
	Nodes            map[string]int `json:"nodes,omitempty"`
	Monitors         map[string]int `json:"monitors,omitempty"`
	OpenIncidents    int            `json:"open_incidents"`
	Incidents        *struct{}      `json:"-"`
	Certificates     map[string]int `json:"certificates,omitempty"`
	Backups          map[string]int `json:"backups,omitempty"`
	Operations24h    map[string]int `json:"operations_24h,omitempty"`
	RecentOps        []RecentOp     `json:"recent_operations,omitempty"`
	PendingApprovals int            `json:"pending_approvals"`
	Sections         []string       `json:"sections"`
}

func (s *Service) counts(ctx context.Context, q string, args ...any) (map[string]int, error) {
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}

func has(ac authctx.AuthContext, perm string) bool { return rbac.Require(ac, perm) == nil }

func (s *Service) Summary(ctx context.Context, ac authctx.AuthContext) (Summary, error) {
	org := ac.OrganizationID
	var out Summary
	var err error
	fail := func(e error) (Summary, error) {
		return Summary{}, apierr.Wrap(apierr.CodeInternal, "failed to build dashboard", e)
	}
	if has(ac, "projects.read") {
		out.Sections = append(out.Sections, "projects")
		if out.Projects, err = s.counts(ctx, `SELECT status, count(*)::int FROM projects WHERE organization_id=$1 AND deleted_at IS NULL GROUP BY status`, org); err != nil {
			return fail(err)
		}
	}
	if has(ac, "infrastructure.read") {
		out.Sections = append(out.Sections, "nodes")
		if out.Nodes, err = s.counts(ctx, `SELECT status, count(*)::int FROM nodes WHERE organization_id=$1 GROUP BY status`, org); err != nil {
			return fail(err)
		}
	}
	if has(ac, "monitoring.read") {
		out.Sections = append(out.Sections, "monitors")
		if out.Monitors, err = s.counts(ctx, `SELECT last_status, count(*)::int FROM monitors WHERE organization_id=$1 AND enabled GROUP BY last_status`, org); err != nil {
			return fail(err)
		}
	}
	if has(ac, "incidents.read") {
		out.Sections = append(out.Sections, "incidents")
		if err = s.pool.QueryRow(ctx, `SELECT count(*)::int FROM incidents WHERE organization_id=$1 AND status NOT IN ('resolved','closed')`, org).Scan(&out.OpenIncidents); err != nil {
			return fail(err)
		}
	}
	if has(ac, "ssl.read") {
		out.Sections = append(out.Sections, "certificates")
		if out.Certificates, err = s.counts(ctx, `SELECT status, count(*)::int FROM certificates WHERE organization_id=$1 GROUP BY status`, org); err != nil {
			return fail(err)
		}
	}
	if has(ac, "backups.read") {
		out.Sections = append(out.Sections, "backups")
		if out.Backups, err = s.counts(ctx, `SELECT status, count(*)::int FROM backups WHERE organization_id=$1 AND status <> 'deleted' AND created_at > now() - interval '30 days' GROUP BY status`, org); err != nil {
			return fail(err)
		}
	}
	if has(ac, "operations.read") {
		out.Sections = append(out.Sections, "operations")
		if out.Operations24h, err = s.counts(ctx, `SELECT status, count(*)::int FROM jobs WHERE organization_id=$1 AND operation IS NOT NULL AND created_at > now() - interval '24 hours' GROUP BY status`, org); err != nil {
			return fail(err)
		}
		rows, err := s.pool.Query(ctx, `SELECT id, operation, status, project_id, created_at FROM jobs WHERE organization_id=$1 AND operation IS NOT NULL ORDER BY created_at DESC LIMIT 8`, org)
		if err != nil {
			return fail(err)
		}
		for rows.Next() {
			var o RecentOp
			if err := rows.Scan(&o.ID, &o.Operation, &o.Status, &o.ProjectID, &o.CreatedAt); err != nil {
				rows.Close()
				return fail(err)
			}
			out.RecentOps = append(out.RecentOps, o)
		}
		rows.Close()
	}
	if has(ac, "approvals.decide") {
		out.Sections = append(out.Sections, "approvals")
		if err = s.pool.QueryRow(ctx, `SELECT count(*)::int FROM approvals WHERE organization_id=$1 AND status='pending'`, org).Scan(&out.PendingApprovals); err != nil {
			return fail(err)
		}
	}
	return out, nil
}

// ---------------- search ----------------

type Hit struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Title string `json:"title"`
	Hint  string `json:"hint,omitempty"`
	Path  string `json:"path"`
}

func like(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// Search finds resources by name across the sections the caller may read.
func (s *Service) Search(ctx context.Context, ac authctx.AuthContext, q string) ([]Hit, error) {
	q = strings.TrimSpace(q)
	if len(q) < 2 {
		return []Hit{}, nil
	}
	if len(q) > 100 {
		q = q[:100]
	}
	org, pat := ac.OrganizationID, like(q)
	hits := []Hit{}
	type src struct {
		perm, typ, sql, path string
	}
	for _, x := range []src{
		{"projects.read", "project", `SELECT id::text, name, slug FROM projects WHERE organization_id=$1 AND deleted_at IS NULL AND (name ILIKE '%'||$2||'%' OR slug ILIKE '%'||$2||'%') ORDER BY name LIMIT 6`, "/projects/"},
		{"clients.read", "client", `SELECT id::text, name, contact_email FROM clients WHERE organization_id=$1 AND deleted_at IS NULL AND name ILIKE '%'||$2||'%' ORDER BY name LIMIT 5`, "/clients/"},
		{"domains.read", "domain", `SELECT id::text, name, status FROM domains WHERE organization_id=$1 AND deleted_at IS NULL AND name ILIKE '%'||$2||'%' ORDER BY name LIMIT 5`, "/domains/"},
		{"applications.read", "application", `SELECT id::text, name, status FROM applications WHERE organization_id=$1 AND name ILIKE '%'||$2||'%' ORDER BY name LIMIT 5`, "/applications/"},
		{"monitoring.read", "monitor", `SELECT id::text, name, kind FROM monitors WHERE organization_id=$1 AND name ILIKE '%'||$2||'%' ORDER BY name LIMIT 5`, "/monitoring/"},
		{"incidents.read", "incident", `SELECT id::text, title, status FROM incidents WHERE organization_id=$1 AND title ILIKE '%'||$2||'%' ORDER BY detected_at DESC LIMIT 5`, "/incidents/"},
		{"infrastructure.read", "node", `SELECT id::text, hostname, status FROM nodes WHERE organization_id=$1 AND hostname ILIKE '%'||$2||'%' ORDER BY hostname LIMIT 5`, "/infrastructure/"},
	} {
		if !has(ac, x.perm) {
			continue
		}
		rows, err := s.pool.Query(ctx, x.sql, org, pat)
		if err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "search failed", err)
		}
		for rows.Next() {
			h := Hit{Type: x.typ}
			if err := rows.Scan(&h.ID, &h.Title, &h.Hint); err != nil {
				rows.Close()
				return nil, err
			}
			h.Path = x.path + h.ID
			hits = append(hits, h)
		}
		rows.Close()
	}
	return hits, nil
}
