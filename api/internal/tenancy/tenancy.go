// Package tenancy implements organizations and membership — the tenant
// boundary that every other domain scopes its data by (ADR-004).
package tenancy

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
)

// systemOwnerRoleID is the seeded 'owner' role from migration 0002 — every
// new organization's creator is granted it directly.
var systemOwnerRoleID = uuid.MustParse("00000000-0000-0000-0000-000000000001")

type Service struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

type Organization struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
}

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// CreateOrganization creates a new organization, adds the creating user as a
// member, and grants them the system 'owner' role. This is the one place a
// user gets their first organization — there is no separate "add first
// member" step.
func (s *Service) CreateOrganization(ctx context.Context, creatorUserID uuid.UUID, name, slug string) (Organization, error) {
	if strings.TrimSpace(name) == "" {
		return Organization{}, apierr.Validation("organization name is required")
	}
	if !slugPattern.MatchString(slug) {
		return Organization{}, apierr.Validation("slug must be lowercase alphanumeric with single hyphens")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Organization{}, apierr.Wrap(apierr.CodeInternal, "failed to begin transaction", err)
	}
	defer tx.Rollback(ctx)

	var org Organization
	err = tx.QueryRow(ctx, `
		INSERT INTO organizations (name, slug) VALUES ($1, $2)
		RETURNING id, name, slug, created_at
	`, name, slug).Scan(&org.ID, &org.Name, &org.Slug, &org.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return Organization{}, apierr.Conflict("an organization with this slug already exists")
		}
		return Organization{}, apierr.Wrap(apierr.CodeInternal, "failed to create organization", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_members (organization_id, user_id) VALUES ($1, $2)
	`, org.ID, creatorUserID); err != nil {
		return Organization{}, apierr.Wrap(apierr.CodeInternal, "failed to add creator as member", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_member_roles (organization_id, user_id, role_id) VALUES ($1, $2, $3)
	`, org.ID, creatorUserID, systemOwnerRoleID); err != nil {
		return Organization{}, apierr.Wrap(apierr.CodeInternal, "failed to grant owner role", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Organization{}, apierr.Wrap(apierr.CodeInternal, "failed to commit transaction", err)
	}

	return org, nil
}

// ListForUser returns every organization a user belongs to. Used right after
// login so a client can offer an organization picker without the user
// needing to already know an org ID.
func (s *Service) ListForUser(ctx context.Context, userID uuid.UUID) ([]Organization, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT o.id, o.name, o.slug, o.created_at
		FROM organizations o
		JOIN organization_members m ON m.organization_id = o.id
		WHERE m.user_id = $1
		ORDER BY o.created_at ASC
	`, userID)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list organizations", err)
	}
	defer rows.Close()

	var out []Organization
	for rows.Next() {
		var o Organization
		if err := rows.Scan(&o.ID, &o.Name, &o.Slug, &o.CreatedAt); err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan organization", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// Get returns an organization by ID, scoped to the caller's own membership —
// this is the pattern every tenant-scoped read follows (ADR-004): the
// AuthContext's OrganizationID, not a client-supplied one, decides scope.
func (s *Service) Get(ctx context.Context, ac authctx.AuthContext) (Organization, error) {
	var o Organization
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, slug, created_at FROM organizations WHERE id = $1
	`, ac.OrganizationID).Scan(&o.ID, &o.Name, &o.Slug, &o.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, apierr.NotFound("organization")
	}
	if err != nil {
		return Organization{}, apierr.Wrap(apierr.CodeInternal, "failed to load organization", err)
	}
	return o, nil
}

func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23505"
	}
	return false
}
