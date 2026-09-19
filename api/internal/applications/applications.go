// Package applications implements the applications/services domain
// (section 10 of the product brief). Deliberately minimal in phase 1:
// registration, inventory, status reporting, and retirement — mirroring
// internal/infrastructure's shape (including its update/status-report/
// decommission-style trio, here Update/UpdateStatus/Deregister).
// Deployment execution belongs to the jobs system once a real deploy
// backend exists (see docs/ROADMAP.md) — this package does not attempt to
// deploy anything; UpdateStatus only records what a caller reports, it
// never drives a deployment itself.
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

type UpdateInput struct {
	Name          *string    `json:"name"`
	Kind          *string    `json:"kind"`
	NodeID        *uuid.UUID `json:"node_id"`
	Environment   *string    `json:"environment"`
	RepositoryURL *string    `json:"repository_url"`
}

// Update edits an application's editable fields — everything except
// status, which is reported separately by UpdateStatus/Deregister. Each
// field is a pointer (nil = leave unchanged); NodeID can be explicitly
// cleared by passing a pointer to uuid.Nil.
func (s *Service) Update(ctx context.Context, ac authctx.AuthContext, id uuid.UUID, in UpdateInput) (Application, error) {
	if err := rbac.Require(ac, permManage); err != nil {
		return Application{}, err
	}

	existing, err := s.Get(ctx, ac, id)
	if err != nil {
		return Application{}, err
	}
	if existing.Status == "deregistered" {
		return Application{}, apierr.Conflict("application is deregistered")
	}

	name := existing.Name
	if in.Name != nil {
		if *in.Name == "" {
			return Application{}, apierr.Validation("name cannot be empty")
		}
		name = *in.Name
	}
	kind := existing.Kind
	if in.Kind != nil {
		kind = *in.Kind
	}
	nodeID := existing.NodeID
	if in.NodeID != nil {
		if *in.NodeID == uuid.Nil {
			nodeID = nil
		} else {
			nodeID = in.NodeID
		}
	}
	environment := existing.Environment
	if in.Environment != nil {
		environment = *in.Environment
	}
	repositoryURL := existing.RepositoryURL
	if in.RepositoryURL != nil {
		repositoryURL = *in.RepositoryURL
	}

	var a Application
	err = s.pool.QueryRow(ctx, `
		UPDATE applications SET name = $3, kind = $4, node_id = $5, environment = $6,
		                         repository_url = NULLIF($7, ''), updated_at = now()
		WHERE id = $1 AND organization_id = $2
		RETURNING id, organization_id, name, kind, node_id, environment, status,
		          COALESCE(repository_url, ''), created_at, updated_at
	`, id, ac.OrganizationID, name, kind, nodeID, environment, repositoryURL).Scan(
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
		return Application{}, apierr.Wrap(apierr.CodeInternal, "failed to update application", err)
	}

	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: "applications.application.updated", ResourceType: "application", ResourceID: a.ID.String(),
		Success: true, PreviousState: existing, ResultingState: a,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}

	return a, nil
}

var validApplicationStatuses = map[string]bool{
	"unknown": true, "running": true, "stopped": true, "degraded": true, "failed": true,
}

// UpdateStatus reports an application's current status — what a future
// deployment/health-check pipeline would call once one exists
// (docs/ROADMAP.md: deployment execution is still PLANNED). Does not
// accept 'deregistered' — that's Deregister's terminal, one-way action.
func (s *Service) UpdateStatus(ctx context.Context, ac authctx.AuthContext, id uuid.UUID, status string) (Application, error) {
	if err := rbac.Require(ac, permManage); err != nil {
		return Application{}, err
	}
	if !validApplicationStatuses[status] {
		return Application{}, apierr.Validation("status must be one of: unknown, running, stopped, degraded, failed")
	}

	existing, err := s.Get(ctx, ac, id)
	if err != nil {
		return Application{}, err
	}
	if existing.Status == "deregistered" {
		return Application{}, apierr.Conflict("application is deregistered")
	}

	var a Application
	err = s.pool.QueryRow(ctx, `
		UPDATE applications SET status = $3, updated_at = now()
		WHERE id = $1 AND organization_id = $2
		RETURNING id, organization_id, name, kind, node_id, environment, status,
		          COALESCE(repository_url, ''), created_at, updated_at
	`, id, ac.OrganizationID, status).Scan(
		&a.ID, &a.OrganizationID, &a.Name, &a.Kind, &a.NodeID, &a.Environment, &a.Status,
		&a.RepositoryURL, &a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		return Application{}, apierr.Wrap(apierr.CodeInternal, "failed to update application status", err)
	}

	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: "applications.application.status_updated", ResourceType: "application", ResourceID: a.ID.String(),
		Success: true, PreviousState: existing, ResultingState: a,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}

	return a, nil
}

// Deregister marks an application as permanently retired — a terminal,
// one-way state (migration 0015). The row is kept, not deleted, so audit
// history stays meaningful. Idempotent: deregistering an
// already-deregistered application is not an error.
func (s *Service) Deregister(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) (Application, error) {
	if err := rbac.Require(ac, permManage); err != nil {
		return Application{}, err
	}

	existing, err := s.Get(ctx, ac, id)
	if err != nil {
		return Application{}, err
	}

	var a Application
	err = s.pool.QueryRow(ctx, `
		UPDATE applications SET status = 'deregistered', updated_at = now()
		WHERE id = $1 AND organization_id = $2
		RETURNING id, organization_id, name, kind, node_id, environment, status,
		          COALESCE(repository_url, ''), created_at, updated_at
	`, id, ac.OrganizationID).Scan(
		&a.ID, &a.OrganizationID, &a.Name, &a.Kind, &a.NodeID, &a.Environment, &a.Status,
		&a.RepositoryURL, &a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		return Application{}, apierr.Wrap(apierr.CodeInternal, "failed to deregister application", err)
	}

	if existing.Status != "deregistered" {
		if err := s.audit.Record(ctx, ac, audit.Entry{
			Action: "applications.application.deregistered", ResourceType: "application", ResourceID: a.ID.String(),
			Success: true, PreviousState: existing, ResultingState: a,
		}); err != nil {
			logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
		}
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
