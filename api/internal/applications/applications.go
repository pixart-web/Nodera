// Package applications implements the applications/services domain
// (section 10 of the product brief). Deliberately minimal in phase 1:
// registration and inventory only, mirroring internal/infrastructure's
// shape. Deployment execution belongs to the jobs system once a real
// deploy backend exists (see docs/ROADMAP.md) — this package does not
// attempt to deploy anything.
package applications

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/platform/logger"
	"github.com/nodera/nodera/internal/rbac"
)

type AuditRecorder interface {
	Record(ctx context.Context, ac authctx.AuthContext, e audit.Entry) error
}

type Service struct {
	pool  *pgxpool.Pool
	audit AuditRecorder
}

func New(pool *pgxpool.Pool, audit AuditRecorder) *Service {
	return &Service{pool: pool, audit: audit}
}

type Application struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID uuid.UUID  `json:"organization_id"`
	Name           string     `json:"name"`
	Kind           string     `json:"kind"`
	NodeID         *uuid.UUID `json:"node_id"`
	Environment    string     `json:"environment"`
	Status         string     `json:"status"`
	RepositoryURL  string     `json:"repository_url"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type RegisterInput struct {
	Name          string     `json:"name"`
	Kind          string     `json:"kind"`
	NodeID        *uuid.UUID `json:"node_id"`
	Environment   string     `json:"environment"`
	RepositoryURL string     `json:"repository_url"`
}

// permission note: the current catalog (migration 0002_rbac.sql) has
// 'applications.read' and 'applications.deploy' but no separate
// 'applications.manage'. Registering an application record is gated by
// applications.deploy — the closest fit — rather than inventing a new
// permission key ad hoc; see docs/API.md.
const permManage = "applications.deploy"
const permRead = "applications.read"

func (s *Service) Register(ctx context.Context, ac authctx.AuthContext, in RegisterInput) (Application, error) {
	if err := rbac.Require(ac, permManage); err != nil {
		return Application{}, err
	}
	if in.Name == "" {
		return Application{}, apierr.Validation("name is required")
	}
	if in.Kind == "" {
		in.Kind = "service"
	}
	if in.Environment == "" {
		in.Environment = "production"
	}

	var a Application
	err := s.pool.QueryRow(ctx, `
		INSERT INTO applications (organization_id, name, kind, node_id, environment, repository_url)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''))
		RETURNING id, organization_id, name, kind, node_id, environment, status,
		          COALESCE(repository_url, ''), created_at, updated_at
	`, ac.OrganizationID, in.Name, in.Kind, in.NodeID, in.Environment, in.RepositoryURL).Scan(
		&a.ID, &a.OrganizationID, &a.Name, &a.Kind, &a.NodeID, &a.Environment, &a.Status,
		&a.RepositoryURL, &a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return Application{}, apierr.Conflict("an application with this name already exists in this organization")
		}
		if isForeignKeyViolation(err) {
			return Application{}, apierr.Validation("node_id does not refer to a node in this organization")
		}
		return Application{}, apierr.Wrap(apierr.CodeInternal, "failed to register application", err)
	}

	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action:         "applications.application.registered",
		ResourceType:   "application",
		ResourceID:     a.ID.String(),
		Success:        true,
		ResultingState: a,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}

	return a, nil
}

// List returns up to limit applications in the caller's organization,
// starting at offset. See infrastructure.Service.List's doc comment for
// why this method enforces its own limit ceiling independent of the HTTP
// layer's.
func (s *Service) List(ctx context.Context, ac authctx.AuthContext, limit, offset int) ([]Application, error) {
	if err := rbac.Require(ac, permRead); err != nil {
		return nil, err
	}
	limit = normalizeLimit(limit)

	rows, err := s.pool.Query(ctx, `
		SELECT id, organization_id, name, kind, node_id, environment, status,
		       COALESCE(repository_url, ''), created_at, updated_at
		FROM applications
		WHERE organization_id = $1
		ORDER BY name ASC
		LIMIT $2 OFFSET $3
	`, ac.OrganizationID, limit, offset)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list applications", err)
	}
	defer rows.Close()

	var out []Application
	for rows.Next() {
		var a Application
		if err := rows.Scan(&a.ID, &a.OrganizationID, &a.Name, &a.Kind, &a.NodeID, &a.Environment,
			&a.Status, &a.RepositoryURL, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan application", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Service) Get(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) (Application, error) {
	if err := rbac.Require(ac, permRead); err != nil {
		return Application{}, err
	}

	var a Application
	err := s.pool.QueryRow(ctx, `
		SELECT id, organization_id, name, kind, node_id, environment, status,
		       COALESCE(repository_url, ''), created_at, updated_at
		FROM applications
		WHERE id = $1 AND organization_id = $2
	`, id, ac.OrganizationID).Scan(&a.ID, &a.OrganizationID, &a.Name, &a.Kind, &a.NodeID,
		&a.Environment, &a.Status, &a.RepositoryURL, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Application{}, apierr.NotFound("application")
	}
	if err != nil {
		return Application{}, apierr.Wrap(apierr.CodeInternal, "failed to load application", err)
	}
	return a, nil
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 1000 {
		return 1000
	}
	return limit
}

func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23505"
	}
	return false
}

func isForeignKeyViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23503"
	}
	return false
}
