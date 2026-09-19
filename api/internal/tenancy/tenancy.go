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

	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/identity"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/platform/logger"
	"github.com/nodera/nodera/internal/rbac"
)

// systemOwnerRoleID / systemMemberRoleID are the seeded system roles from
// migration 0002. The creator of an organization is granted owner directly;
// AddMember grants member (the caller can use rbac.AssignRole afterward to
// grant anything broader — no privilege escalation happens here).
var systemOwnerRoleID = uuid.MustParse("00000000-0000-0000-0000-000000000001")
var systemMemberRoleID = uuid.MustParse("00000000-0000-0000-0000-000000000003")

type AuditRecorder interface {
	Record(ctx context.Context, ac authctx.AuthContext, e audit.Entry) error
}

type Service struct {
	pool     *pgxpool.Pool
	identity *identity.Service
	audit    AuditRecorder
}

func New(pool *pgxpool.Pool, identitySvc *identity.Service, auditRecorder AuditRecorder) *Service {
	return &Service{pool: pool, identity: identitySvc, audit: auditRecorder}
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

// UpdateOrganizationInput follows the pointer-based partial-update
// convention used across the codebase: a nil field leaves the existing
// value untouched.
type UpdateOrganizationInput struct {
	Name *string `json:"name"`
	Slug *string `json:"slug"`
}

// UpdateOrganization renames the calling organization and/or changes its
// slug. Reuses CreateOrganization's own validation (non-empty name, slug
// format) so the two paths can never disagree about what a valid
// name/slug looks like.
func (s *Service) UpdateOrganization(ctx context.Context, ac authctx.AuthContext, in UpdateOrganizationInput) (Organization, error) {
	if err := rbac.Require(ac, "organization.manage"); err != nil {
		return Organization{}, err
	}

	existing, err := s.Get(ctx, ac)
	if err != nil {
		return Organization{}, err
	}

	name := existing.Name
	if in.Name != nil {
		if strings.TrimSpace(*in.Name) == "" {
			return Organization{}, apierr.Validation("organization name is required")
		}
		name = *in.Name
	}
	slug := existing.Slug
	if in.Slug != nil {
		if !slugPattern.MatchString(*in.Slug) {
			return Organization{}, apierr.Validation("slug must be lowercase alphanumeric with single hyphens")
		}
		slug = *in.Slug
	}

	var org Organization
	err = s.pool.QueryRow(ctx, `
		UPDATE organizations SET name = $1, slug = $2 WHERE id = $3
		RETURNING id, name, slug, created_at
	`, name, slug, ac.OrganizationID).Scan(&org.ID, &org.Name, &org.Slug, &org.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return Organization{}, apierr.Conflict("an organization with this slug already exists")
		}
		return Organization{}, apierr.Wrap(apierr.CodeInternal, "failed to update organization", err)
	}

	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: "tenancy.organization.updated", ResourceType: "organization", ResourceID: org.ID.String(),
		Success: true, PreviousState: existing, ResultingState: org,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}

	return org, nil
}

// AddMember adds an existing user (looked up by email) to the calling
// organization and grants them the system 'member' role — the same
// starting point CreateOrganization gives itself no special treatment
// beyond, except the creator also gets 'owner'. It does not create a user
// account: the email must belong to someone who already signed up
// (internal/identity.SignUp is the only place accounts are created), so
// this is "join an existing user to this org," not "invite by email" in
// the send-a-link sense — there is no email delivery in this phase
// (docs/ROADMAP.md).
func (s *Service) AddMember(ctx context.Context, ac authctx.AuthContext, email string) (AddedMember, error) {
	if err := rbac.Require(ac, "organization.manage"); err != nil {
		return AddedMember{}, err
	}

	u, err := s.identity.FindByEmail(ctx, email)
	if err != nil {
		return AddedMember{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AddedMember{}, apierr.Wrap(apierr.CodeInternal, "failed to begin transaction", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_members (organization_id, user_id) VALUES ($1, $2)
	`, ac.OrganizationID, u.ID); err != nil {
		if isUniqueViolation(err) {
			return AddedMember{}, apierr.Conflict("user is already a member of this organization")
		}
		return AddedMember{}, apierr.Wrap(apierr.CodeInternal, "failed to add member", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_member_roles (organization_id, user_id, role_id) VALUES ($1, $2, $3)
	`, ac.OrganizationID, u.ID, systemMemberRoleID); err != nil {
		return AddedMember{}, apierr.Wrap(apierr.CodeInternal, "failed to grant member role", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return AddedMember{}, apierr.Wrap(apierr.CodeInternal, "failed to commit transaction", err)
	}

	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: "tenancy.member.added", ResourceType: "organization_member", ResourceID: u.ID.String(),
		Success: true, ResultingState: u,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}

	return AddedMember{UserID: u.ID, Email: u.Email, DisplayName: u.DisplayName}, nil
}

// RemoveMember removes an existing member from the calling organization —
// the missing counterpart to AddMember. The composite FK on
// organization_member_roles (migration 0002) cascades on delete, so a
// single DELETE from organization_members is enough to also drop every
// role grant the member held; there is nothing else in the schema keyed
// by (organization_id, user_id) today.
//
// Refuses to remove the organization's last remaining holder of the
// system 'owner' role — an org with zero owners has no one left who can
// manage it (assign roles, add/remove members, etc.), an unrecoverable
// state nothing else in the codebase can repair short of a database edit.
func (s *Service) RemoveMember(ctx context.Context, ac authctx.AuthContext, userID uuid.UUID) error {
	if err := rbac.Require(ac, "organization.manage"); err != nil {
		return err
	}
	return s.removeMember(ctx, ac, userID, "tenancy.member.removed")
}

// LeaveOrganization removes the caller themselves from the calling
// organization — the self-service counterpart to the admin-driven
// RemoveMember. Deliberately requires no permission beyond being an
// authenticated member of the org: a member who lacks organization.manage
// can still remove themselves (they just can't remove anyone else), the
// same "you can always act on your own resource" pattern RevokeSession and
// RevokeAPIToken already follow. Subject to the identical last-owner guard
// RemoveMember enforces — a sole owner can't leave any more than they
// could remove themselves via the admin path.
func (s *Service) LeaveOrganization(ctx context.Context, ac authctx.AuthContext) error {
	return s.removeMember(ctx, ac, ac.ActorID, "tenancy.member.left")
}

// removeMember is shared by RemoveMember and LeaveOrganization — same
// last-owner guard, same cascade-via-FK delete, different audit action
// label so the trail records which path was taken (an admin removing
// someone else vs. a member removing themselves).
func (s *Service) removeMember(ctx context.Context, ac authctx.AuthContext, userID uuid.UUID, auditAction string) error {
	var isOwner bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM organization_member_roles
			WHERE organization_id = $1 AND user_id = $2 AND role_id = $3
		)
	`, ac.OrganizationID, userID, systemOwnerRoleID).Scan(&isOwner); err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to check member's role", err)
	}
	if isOwner {
		var ownerCount int
		if err := s.pool.QueryRow(ctx, `
			SELECT count(*) FROM organization_member_roles
			WHERE organization_id = $1 AND role_id = $2
		`, ac.OrganizationID, systemOwnerRoleID).Scan(&ownerCount); err != nil {
			return apierr.Wrap(apierr.CodeInternal, "failed to count owners", err)
		}
		if ownerCount <= 1 {
			return apierr.Conflict("cannot remove the organization's last owner")
		}
	}

	tag, err := s.pool.Exec(ctx, `
		DELETE FROM organization_members WHERE organization_id = $1 AND user_id = $2
	`, ac.OrganizationID, userID)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to remove member", err)
	}
	if tag.RowsAffected() == 0 {
		return apierr.NotFound("organization member")
	}

	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: auditAction, ResourceType: "organization_member", ResourceID: userID.String(),
		Success: true,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}

	return nil
}

// AddedMember is the minimal shape AddMember returns — deliberately not
// identity.User verbatim (this package doesn't own user records, ADR-002;
// it only reports who was just added, not the full user record).
type AddedMember struct {
	UserID      uuid.UUID `json:"user_id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
}

func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23505"
	}
	return false
}
