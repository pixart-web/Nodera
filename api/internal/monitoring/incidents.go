package monitoring

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/platform/logger"
	"github.com/nodera/nodera/internal/rbac"
)

var conditions = map[string]bool{"cpu_above": true, "ram_above": true, "disk_above": true, "http_failure": true, "ssl_expiry_days": true, "container_unhealthy": true, "database_unavailable": true}
var severities = map[string]bool{"info": true, "warning": true, "critical": true}

type Rule struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Condition string    `json:"condition"`
	Threshold float64   `json:"threshold"`
	Severity  string    `json:"severity"`
	Channels  []string  `json:"channels"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

type RuleInput struct {
	Name      string   `json:"name"`
	Condition string   `json:"condition"`
	Threshold float64  `json:"threshold"`
	Severity  string   `json:"severity"`
	Channels  []string `json:"channels"`
	Enabled   *bool    `json:"enabled"`
}

func (s *Service) CreateRule(ctx context.Context, ac authctx.AuthContext, in RuleInput) (Rule, error) {
	if err := rbac.Require(ac, "monitoring.manage"); err != nil {
		return Rule{}, err
	}
	if in.Name == "" || len(in.Name) > 100 {
		return Rule{}, apierr.Validation("name is required")
	}
	if !conditions[in.Condition] {
		return Rule{}, apierr.Validation("unknown condition")
	}
	if in.Severity == "" {
		in.Severity = "warning"
	}
	if !severities[in.Severity] {
		return Rule{}, apierr.Validation("severity must be info, warning or critical")
	}
	switch in.Condition {
	case "cpu_above", "ram_above", "disk_above":
		if in.Threshold <= 0 || in.Threshold > 100 {
			return Rule{}, apierr.Validation("threshold must be a percentage between 0 and 100")
		}
	case "ssl_expiry_days":
		if in.Threshold < 1 || in.Threshold > 365 {
			return Rule{}, apierr.Validation("threshold must be between 1 and 365 days")
		}
	}
	if len(in.Channels) == 0 {
		in.Channels = []string{"in_app"}
	}
	enabled := in.Enabled == nil || *in.Enabled
	var r Rule
	err := s.pool.QueryRow(ctx, `INSERT INTO alert_rules (organization_id, name, condition, threshold, severity, channels, enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, name, condition, threshold, severity, channels, enabled, created_at`,
		ac.OrganizationID, in.Name, in.Condition, in.Threshold, in.Severity, in.Channels, enabled).
		Scan(&r.ID, &r.Name, &r.Condition, &r.Threshold, &r.Severity, &r.Channels, &r.Enabled, &r.CreatedAt)
	if err != nil {
		return Rule{}, apierr.Conflict("a rule with this name already exists")
	}
	s.rec(ctx, ac, "monitoring.rule.created", "alert_rule", r.ID.String(), r)
	return r, nil
}

func (s *Service) ListRules(ctx context.Context, ac authctx.AuthContext) ([]Rule, error) {
	if err := rbac.Require(ac, "monitoring.read"); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id, name, condition, threshold, severity, channels, enabled, created_at FROM alert_rules WHERE organization_id=$1 ORDER BY name`, ac.OrganizationID)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list rules", err)
	}
	defer rows.Close()
	out := []Rule{}
	for rows.Next() {
		var r Rule
		if err := rows.Scan(&r.ID, &r.Name, &r.Condition, &r.Threshold, &r.Severity, &r.Channels, &r.Enabled, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Service) DeleteRule(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) error {
	if err := rbac.Require(ac, "monitoring.manage"); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM alert_rules WHERE id=$1 AND organization_id=$2`, id, ac.OrganizationID)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to delete rule", err)
	}
	if tag.RowsAffected() == 0 {
		return apierr.NotFound("rule")
	}
	s.rec(ctx, ac, "monitoring.rule.deleted", "alert_rule", id.String(), nil)
	return nil
}

// ---------------- evaluation ----------------

// breach is one currently-true alert condition.
type breach struct {
	dedupe       string
	title        string
	resourceType string
	resourceID   string
	projectID    *uuid.UUID
	value        float64
}

func (s *Service) breaches(ctx context.Context, org uuid.UUID, r Rule) ([]breach, error) {
	var out []breach
	switch r.Condition {
	case "cpu_above", "ram_above", "disk_above":
		metric := map[string]string{"cpu_above": "cpu", "ram_above": "ram", "disk_above": "disk"}[r.Condition]
		rows, err := s.pool.Query(ctx, `SELECT DISTINCT ON (ms.node_id) ms.node_id, n.hostname, ms.value FROM metric_samples ms JOIN nodes n ON n.id = ms.node_id
			WHERE ms.organization_id=$1 AND ms.metric=$2 AND ms.sampled_at > now() - interval '10 minutes' ORDER BY ms.node_id, ms.sampled_at DESC`, org, metric)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var host string
			var v float64
			if err := rows.Scan(&id, &host, &v); err != nil {
				return nil, err
			}
			if v > r.Threshold {
				out = append(out, breach{dedupe: fmt.Sprintf("%s:%s", r.ID, id), title: fmt.Sprintf("%s on %s is %.0f%% (limit %.0f%%)", metric, host, v, r.Threshold), resourceType: "node", resourceID: id.String(), value: v})
			}
		}
		return out, rows.Err()
	case "http_failure", "container_unhealthy", "database_unavailable":
		kind := map[string]string{"http_failure": "http", "container_unhealthy": "container", "database_unavailable": "database"}[r.Condition]
		rows, err := s.pool.Query(ctx, `SELECT id, project_id, name, COALESCE(last_error,'') FROM monitors WHERE organization_id=$1 AND enabled AND kind=$2 AND last_status='failing'`, org, kind)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var pid *uuid.UUID
			var name, e string
			if err := rows.Scan(&id, &pid, &name, &e); err != nil {
				return nil, err
			}
			out = append(out, breach{dedupe: fmt.Sprintf("%s:%s", r.ID, id), title: fmt.Sprintf("%s is failing: %s", name, e), resourceType: "monitor", resourceID: id.String(), projectID: pid, value: 1})
		}
		return out, rows.Err()
	case "ssl_expiry_days":
		rows, err := s.pool.Query(ctx, `SELECT c.id, d.name, d.project_id, EXTRACT(EPOCH FROM (c.not_after - now()))/86400 FROM certificates c JOIN domains d ON d.id=c.domain_id
			WHERE c.organization_id=$1 AND c.status IN ('valid','expiring') AND c.not_after < now() + make_interval(days => $2::int)`, org, int(r.Threshold))
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var name string
			var pid *uuid.UUID
			var days float64
			if err := rows.Scan(&id, &name, &pid, &days); err != nil {
				return nil, err
			}
			out = append(out, breach{dedupe: fmt.Sprintf("%s:%s", r.ID, id), title: fmt.Sprintf("certificate for %s expires in %.0f days", name, days), resourceType: "certificate", resourceID: id.String(), projectID: pid, value: days})
		}
		return out, rows.Err()
	}
	return nil, nil
}

// Evaluate checks every enabled rule: new breaches open (or re-use) exactly
// one live incident per dedupe key and notify once; breaches that cleared
// resolve their incident automatically. It returns (opened, resolved).
func (s *Service) Evaluate(ctx context.Context) (int, int, error) {
	rows, err := s.pool.Query(ctx, `SELECT organization_id, id, name, condition, threshold, severity, channels FROM alert_rules WHERE enabled`)
	if err != nil {
		return 0, 0, err
	}
	type rr struct {
		org uuid.UUID
		r   Rule
	}
	var rules []rr
	for rows.Next() {
		var x rr
		if err := rows.Scan(&x.org, &x.r.ID, &x.r.Name, &x.r.Condition, &x.r.Threshold, &x.r.Severity, &x.r.Channels); err != nil {
			rows.Close()
			return 0, 0, err
		}
		rules = append(rules, x)
	}
	rows.Close()
	opened, resolved := 0, 0
	for _, x := range rules {
		bs, err := s.breaches(ctx, x.org, x.r)
		if err != nil {
			logger.FromContext(ctx).Error("rule evaluation failed", "rule", x.r.ID, "error", err)
			continue
		}
		active := map[string]bool{}
		for _, b := range bs {
			active[b.dedupe] = true
			var id uuid.UUID
			err := s.pool.QueryRow(ctx, `INSERT INTO incidents (organization_id, project_id, rule_id, title, severity, resource_type, resource_id, dedupe_key)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (organization_id, dedupe_key) WHERE dedupe_key IS NOT NULL AND status NOT IN ('resolved','closed') DO NOTHING RETURNING id`,
				x.org, b.projectID, x.r.ID, truncate(b.title, 300), x.r.Severity, b.resourceType, b.resourceID, b.dedupe).Scan(&id)
			if errors.Is(err, pgx.ErrNoRows) {
				continue // already open: no duplicate incident, no duplicate notification
			}
			if err != nil {
				logger.FromContext(ctx).Error("incident insert failed", "error", err)
				continue
			}
			opened++
			s.event(ctx, x.org, id, "opened", "Alert rule "+x.r.Name+" fired", "system")
			_, _ = s.pool.Exec(ctx, `INSERT INTO alerts (organization_id, rule_id, incident_id, value) VALUES ($1,$2,$3,$4)`, x.org, x.r.ID, id, b.value)
			if s.notify != nil {
				for _, f := range s.notify.Dispatch(ctx, x.org, x.r.Channels, "incident.opened", "["+x.r.Severity+"] "+b.title, "Rule: "+x.r.Name, "incident", id.String()) {
					s.event(ctx, x.org, id, "delivery_failed", f, "system")
				}
			}
		}
		// Resolve live incidents of this rule whose breach is gone.
		live, err := s.pool.Query(ctx, `SELECT id, dedupe_key, title FROM incidents WHERE organization_id=$1 AND rule_id=$2 AND status NOT IN ('resolved','closed')`, x.org, x.r.ID)
		if err != nil {
			continue
		}
		type li struct {
			id    uuid.UUID
			key   *string
			title string
		}
		var lis []li
		for live.Next() {
			var l li
			if live.Scan(&l.id, &l.key, &l.title) == nil {
				lis = append(lis, l)
			}
		}
		live.Close()
		for _, l := range lis {
			if l.key != nil && !active[*l.key] {
				if _, err := s.pool.Exec(ctx, `UPDATE incidents SET status='resolved', resolved_at=now() WHERE id=$1 AND status NOT IN ('resolved','closed')`, l.id); err == nil {
					resolved++
					_, _ = s.pool.Exec(ctx, `UPDATE alerts SET status='resolved', resolved_at=now() WHERE incident_id=$1`, l.id)
					s.event(ctx, x.org, l.id, "auto_resolved", "The condition cleared; incident resolved automatically", "system")
					if s.notify != nil {
						s.notify.Dispatch(ctx, x.org, x.r.Channels, "incident.resolved", "Resolved: "+l.title, "", "incident", l.id.String())
					}
				}
			}
		}
	}
	return opened, resolved, nil
}

func (s *Service) event(ctx context.Context, org, incident uuid.UUID, kind, msg, actor string) {
	_, _ = s.pool.Exec(ctx, `INSERT INTO incident_events (incident_id, organization_id, kind, message, actor_label) VALUES ($1,$2,$3,$4,$5)`, incident, org, kind, truncate(msg, 500), actor)
}

// ---------------- incident lifecycle ----------------

type Incident struct {
	ID             uuid.UUID  `json:"id"`
	ProjectID      *uuid.UUID `json:"project_id"`
	RuleID         *uuid.UUID `json:"rule_id"`
	Title          string     `json:"title"`
	Severity       string     `json:"severity"`
	Status         string     `json:"status"`
	ResourceType   string     `json:"resource_type"`
	ResourceID     string     `json:"resource_id"`
	DetectedAt     time.Time  `json:"detected_at"`
	AcknowledgedAt *time.Time `json:"acknowledged_at"`
	ResolvedAt     *time.Time `json:"resolved_at"`
	ClosedAt       *time.Time `json:"closed_at"`
	Events         []Event    `json:"events,omitempty"`
}
type Event struct {
	Kind    string    `json:"kind"`
	Message string    `json:"message"`
	Actor   string    `json:"actor"`
	At      time.Time `json:"at"`
}

const incCols = `id, project_id, rule_id, title, severity, status, resource_type, resource_id, detected_at, acknowledged_at, resolved_at, closed_at`

func scanInc(row pgx.Row) (Incident, error) {
	var i Incident
	err := row.Scan(&i.ID, &i.ProjectID, &i.RuleID, &i.Title, &i.Severity, &i.Status, &i.ResourceType, &i.ResourceID, &i.DetectedAt, &i.AcknowledgedAt, &i.ResolvedAt, &i.ClosedAt)
	return i, err
}

func (s *Service) ListIncidents(ctx context.Context, ac authctx.AuthContext, status string, projectID *uuid.UUID, limit, offset int) ([]Incident, error) {
	if err := rbac.Require(ac, "incidents.read"); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT `+incCols+` FROM incidents WHERE organization_id=$1 AND ($2='' OR status=$2) AND ($3::uuid IS NULL OR project_id=$3)
		ORDER BY detected_at DESC LIMIT $4 OFFSET $5`, ac.OrganizationID, status, projectID, limit, offset)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list incidents", err)
	}
	defer rows.Close()
	out := []Incident{}
	for rows.Next() {
		i, err := scanInc(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *Service) GetIncident(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) (Incident, error) {
	if err := rbac.Require(ac, "incidents.read"); err != nil {
		return Incident{}, err
	}
	i, err := scanInc(s.pool.QueryRow(ctx, `SELECT `+incCols+` FROM incidents WHERE id=$1 AND organization_id=$2`, id, ac.OrganizationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Incident{}, apierr.NotFound("incident")
	}
	if err != nil {
		return Incident{}, err
	}
	rows, err := s.pool.Query(ctx, `SELECT kind, message, actor_label, created_at FROM incident_events WHERE incident_id=$1 ORDER BY id`, id)
	if err != nil {
		return Incident{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.Kind, &e.Message, &e.Actor, &e.At); err != nil {
			return Incident{}, err
		}
		i.Events = append(i.Events, e)
	}
	return i, rows.Err()
}

var transitions = map[string]map[string]bool{
	"acknowledge": {"open": true},
	"investigate": {"open": true, "acknowledged": true},
	"resolve":     {"open": true, "acknowledged": true, "investigating": true},
	"close":       {"resolved": true},
}
var target = map[string]string{"acknowledge": "acknowledged", "investigate": "investigating", "resolve": "resolved", "close": "closed"}

// Transition moves an incident through its lifecycle (acknowledge,
// investigate, resolve, close). Illegal transitions are a 409.
func (s *Service) Transition(ctx context.Context, ac authctx.AuthContext, id uuid.UUID, action, note string) (Incident, error) {
	if err := rbac.Require(ac, "incidents.manage"); err != nil {
		return Incident{}, err
	}
	allowed, ok := transitions[action]
	if !ok {
		return Incident{}, apierr.Validation("action must be acknowledge, investigate, resolve or close")
	}
	if len(note) > 1000 {
		return Incident{}, apierr.Validation("note too long")
	}
	var cur string
	if err := s.pool.QueryRow(ctx, `SELECT status FROM incidents WHERE id=$1 AND organization_id=$2`, id, ac.OrganizationID).Scan(&cur); err != nil {
		return Incident{}, apierr.NotFound("incident")
	}
	if !allowed[cur] {
		return Incident{}, apierr.Conflict(fmt.Sprintf("cannot %s an incident that is %s", action, cur))
	}
	next := target[action]
	tag, err := s.pool.Exec(ctx, `UPDATE incidents SET status=$3,
		acknowledged_at = CASE WHEN $3='acknowledged' THEN now() ELSE acknowledged_at END,
		resolved_at = CASE WHEN $3='resolved' THEN now() ELSE resolved_at END,
		closed_at = CASE WHEN $3='closed' THEN now() ELSE closed_at END
		WHERE id=$1 AND organization_id=$2 AND status=$4`, id, ac.OrganizationID, next, cur)
	if err != nil {
		return Incident{}, apierr.Wrap(apierr.CodeInternal, "failed to update incident", err)
	}
	if tag.RowsAffected() == 0 {
		return Incident{}, apierr.Conflict("incident changed concurrently; reload and retry")
	}
	actor := ac.ActorLabel
	if actor == "" {
		actor = "user"
	}
	msg := action
	if note != "" {
		msg += ": " + note
	}
	s.event(ctx, ac.OrganizationID, id, next, msg, actor)
	s.rec(ctx, ac, "incidents.incident."+next, "incident", id.String(), map[string]any{"from": cur, "to": next})
	return s.GetIncident(ctx, ac, id)
}
