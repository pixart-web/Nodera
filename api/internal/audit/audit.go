// Package audit implements Nodera's audit log (section 6). It exposes only
// Record (insert) and Query (read) — there is deliberately no Update or
// Delete method, so accidental tampering isn't even representable in the Go
// API surface, let alone reachable through it.
package audit

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
)

type Service struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// Entry describes a single audit event to record. PreviousState and
// ResultingState are optional and, when present, must never contain secret
// values (docs/SECURITY.md) — callers are responsible for redaction before
// passing state in here; the audit module does not attempt to guess which
// fields are sensitive.
type Entry struct {
	Action         string
	ResourceType   string
	ResourceID     string
	Source         string // "api" | "agent" | "system"; defaults to "api"
	Success        bool
	PreviousState  any
	ResultingState any
	Metadata       map[string]any
}

// Record writes an audit entry attributed to ac. It never returns an error
// that should block the caller's primary operation from being reported as
// successful to the user; callers should log (not fail the request) if
// Record itself errors, since a missing audit row is a monitoring problem,
// not a reason to roll back a legitimate action. That tradeoff is made
// explicit here rather than silently — callers decide how to handle the
// returned error.
func (s *Service) Record(ctx context.Context, ac authctx.AuthContext, e Entry) error {
	source := e.Source
	if source == "" {
		source = "api"
	}

	var actorUserID, actorServiceAccountID *uuid.UUID
	switch ac.ActorType {
	case authctx.ActorUser:
		id := ac.ActorID
		actorUserID = &id
	case authctx.ActorServiceAccount:
		id := ac.ActorID
		actorServiceAccountID = &id
	}

	prevJSON, err := marshalNullable(e.PreviousState)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to marshal previous_state", err)
	}
	resultJSON, err := marshalNullable(e.ResultingState)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to marshal resulting_state", err)
	}
	metadata := e.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to marshal metadata", err)
	}

	var orgID *uuid.UUID
	if ac.OrganizationID != uuid.Nil {
		id := ac.OrganizationID
		orgID = &id
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO audit_log (
			organization_id, actor_user_id, actor_service_account_id, actor_label,
			action, resource_type, resource_id, source, correlation_id, success,
			previous_state, resulting_state, metadata
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
	`,
		orgID, actorUserID, actorServiceAccountID, ac.ActorLabel,
		e.Action, e.ResourceType, nullableString(e.ResourceID), source,
		nullableString(ac.CorrelationID), e.Success,
		prevJSON, resultJSON, metadataJSON,
	)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to record audit entry", err)
	}
	return nil
}

// Record types returned by Query.
type Record struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID *uuid.UUID `json:"organization_id"`
	ActorLabel     string     `json:"actor_label"`
	Action         string     `json:"action"`
	ResourceType   string     `json:"resource_type"`
	ResourceID     string     `json:"resource_id"`
	Source         string     `json:"source"`
	CorrelationID  string     `json:"correlation_id"`
	Success        bool       `json:"success"`
	CreatedAt      time.Time  `json:"created_at"`
}

// QueryFilter narrows a Query call. OrganizationID is required — audit.Query
// never returns cross-tenant results (ADR-004). From/To narrow by
// created_at, both inclusive at their respective ends; either or both may
// be zero-valued (time.Time{}) to leave that bound open. Without a
// time-range filter, investigating "what happened around this incident"
// on an organization with a lot of history means paging through
// everything else first.
type QueryFilter struct {
	OrganizationID uuid.UUID
	ResourceType   string // optional
	ResourceID     string // optional
	Action         string // optional, exact match
	From           time.Time
	To             time.Time
	Limit          int
	Offset         int
}

func (s *Service) Query(ctx context.Context, ac authctx.AuthContext, f QueryFilter) ([]Record, error) {
	if err := requirePermission(ac); err != nil {
		return nil, err
	}
	// See infrastructure.Service.List's doc comment for why this cap (1000)
	// is independent of, and higher than, the HTTP layer's own cap
	// (internal/platform/httpserver.MaxPageLimit) — it must not interfere
	// with that layer's limit+1 over-fetch-to-detect-more-pages trick.
	if f.Limit <= 0 {
		f.Limit = 50
	} else if f.Limit > 1000 {
		f.Limit = 1000
	}
	if !f.From.IsZero() && !f.To.IsZero() && f.From.After(f.To) {
		return nil, apierr.Validation("from must not be after to")
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, organization_id, actor_label, action, resource_type,
		       COALESCE(resource_id, ''), source, COALESCE(correlation_id, ''), success, created_at
		FROM audit_log
		WHERE organization_id = $1
		  AND ($2 = '' OR resource_type = $2)
		  AND ($3 = '' OR resource_id = $3)
		  AND ($4 = '' OR action = $4)
		  AND ($5::timestamptz IS NULL OR created_at >= $5)
		  AND ($6::timestamptz IS NULL OR created_at <= $6)
		ORDER BY created_at DESC
		LIMIT $7 OFFSET $8
	`, ac.OrganizationID, f.ResourceType, f.ResourceID, f.Action,
		nullableTime(f.From), nullableTime(f.To), f.Limit, f.Offset)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to query audit log", err)
	}
	defer rows.Close()

	var out []Record
	for rows.Next() {
		var r Record
		if err := rows.Scan(&r.ID, &r.OrganizationID, &r.ActorLabel, &r.Action, &r.ResourceType,
			&r.ResourceID, &r.Source, &r.CorrelationID, &r.Success, &r.CreatedAt); err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan audit record", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func requirePermission(ac authctx.AuthContext) error {
	if ac.ActorType == authctx.ActorSystem {
		return nil
	}
	if !ac.HasPermission("audit.read") {
		return apierr.Forbidden("missing required permission: audit.read")
	}
	return nil
}

func marshalNullable(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nullableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
