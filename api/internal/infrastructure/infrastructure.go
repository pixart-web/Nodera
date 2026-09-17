// Package infrastructure implements the provider-agnostic node model
// (sections 7-8). It intentionally has zero knowledge of any specific
// hosting provider — provider-specific data lives in the opaque
// ProviderData map (ADR: see docs/DECISIONS.md and docs/INFRASTRUCTURE.md).
package infrastructure

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

type Node struct {
	ID                 uuid.UUID  `json:"id"`
	OrganizationID     uuid.UUID  `json:"organization_id"`
	Hostname           string     `json:"hostname"`
	Provider           string     `json:"provider"`
	ProviderResourceID string     `json:"provider_resource_id"`
	Role               string     `json:"role"`
	Environment        string     `json:"environment"`
	Status             string     `json:"status"`
	OperatingSystem    string     `json:"operating_system"`
	CPUCores           int        `json:"cpu_cores"`
	MemoryMB           int        `json:"memory_mb"`
	StorageGB          int        `json:"storage_gb"`
	Capabilities       []string   `json:"capabilities"`
	LastSeenAt         *time.Time `json:"last_seen_at"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type RegisterNodeInput struct {
	Hostname     string   `json:"hostname"`
	Provider     string   `json:"provider"`
	Role         string   `json:"role"`
	Environment  string   `json:"environment"`
	Capabilities []string `json:"capabilities"`
}

// RegisterNode records a new node in the inventory. It does not attempt to
// contact the node or verify it exists — that is the future Node Agent's
// job (docs/ARCHITECTURE.md §2); this is pure inventory state.
func (s *Service) RegisterNode(ctx context.Context, ac authctx.AuthContext, in RegisterNodeInput) (Node, error) {
	if err := rbac.Require(ac, "infrastructure.manage"); err != nil {
		return Node{}, err
	}
	if in.Hostname == "" {
		return Node{}, apierr.Validation("hostname is required")
	}
	if in.Provider == "" {
		in.Provider = "unknown"
	}
	if in.Role == "" {
		in.Role = "application"
	}
	if in.Environment == "" {
		in.Environment = "production"
	}
	if in.Capabilities == nil {
		in.Capabilities = []string{}
	}

	var n Node
	err := s.pool.QueryRow(ctx, `
		INSERT INTO nodes (organization_id, hostname, provider, role, environment, capabilities)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, organization_id, hostname, provider, COALESCE(provider_resource_id, ''),
		          role, environment, status, COALESCE(operating_system, ''),
		          COALESCE(cpu_cores, 0), COALESCE(memory_mb, 0), COALESCE(storage_gb, 0),
		          capabilities, last_seen_at, created_at, updated_at
	`, ac.OrganizationID, in.Hostname, in.Provider, in.Role, in.Environment, in.Capabilities).Scan(
		&n.ID, &n.OrganizationID, &n.Hostname, &n.Provider, &n.ProviderResourceID,
		&n.Role, &n.Environment, &n.Status, &n.OperatingSystem,
		&n.CPUCores, &n.MemoryMB, &n.StorageGB, &n.Capabilities, &n.LastSeenAt, &n.CreatedAt, &n.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return Node{}, apierr.Conflict("a node with this hostname already exists in this organization")
		}
		return Node{}, apierr.Wrap(apierr.CodeInternal, "failed to register node", err)
	}

	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action:         "infrastructure.node.registered",
		ResourceType:   "node",
		ResourceID:     n.ID.String(),
		Success:        true,
		ResultingState: n,
	}); err != nil {
		logAuditFailure(ctx, err)
	}

	return n, nil
}

// List returns every node in the caller's organization. Tenant scoping comes
// entirely from ac.OrganizationID (ADR-004) — there is no filter parameter
// that could widen the query.
func (s *Service) List(ctx context.Context, ac authctx.AuthContext) ([]Node, error) {
	if err := rbac.Require(ac, "infrastructure.read"); err != nil {
		return nil, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, organization_id, hostname, provider, COALESCE(provider_resource_id, ''),
		       role, environment, status, COALESCE(operating_system, ''),
		       COALESCE(cpu_cores, 0), COALESCE(memory_mb, 0), COALESCE(storage_gb, 0),
		       capabilities, last_seen_at, created_at, updated_at
		FROM nodes
		WHERE organization_id = $1
		ORDER BY hostname ASC
	`, ac.OrganizationID)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list nodes", err)
	}
	defer rows.Close()

	var out []Node
	for rows.Next() {
		var n Node
		if err := rows.Scan(
			&n.ID, &n.OrganizationID, &n.Hostname, &n.Provider, &n.ProviderResourceID,
			&n.Role, &n.Environment, &n.Status, &n.OperatingSystem,
			&n.CPUCores, &n.MemoryMB, &n.StorageGB, &n.Capabilities, &n.LastSeenAt, &n.CreatedAt, &n.UpdatedAt,
		); err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan node", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Service) Get(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) (Node, error) {
	if err := rbac.Require(ac, "infrastructure.read"); err != nil {
		return Node{}, err
	}

	var n Node
	err := s.pool.QueryRow(ctx, `
		SELECT id, organization_id, hostname, provider, COALESCE(provider_resource_id, ''),
		       role, environment, status, COALESCE(operating_system, ''),
		       COALESCE(cpu_cores, 0), COALESCE(memory_mb, 0), COALESCE(storage_gb, 0),
		       capabilities, last_seen_at, created_at, updated_at
		FROM nodes
		WHERE id = $1 AND organization_id = $2
	`, id, ac.OrganizationID).Scan(
		&n.ID, &n.OrganizationID, &n.Hostname, &n.Provider, &n.ProviderResourceID,
		&n.Role, &n.Environment, &n.Status, &n.OperatingSystem,
		&n.CPUCores, &n.MemoryMB, &n.StorageGB, &n.Capabilities, &n.LastSeenAt, &n.CreatedAt, &n.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Node{}, apierr.NotFound("node")
	}
	if err != nil {
		return Node{}, apierr.Wrap(apierr.CodeInternal, "failed to load node", err)
	}
	return n, nil
}

func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23505"
	}
	return false
}

// logAuditFailure handles the deliberate tradeoff documented on
// audit.Service.Record: a failure to write an audit row must not fail the
// primary operation, but must not vanish silently either.
func logAuditFailure(ctx context.Context, err error) {
	logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
}
