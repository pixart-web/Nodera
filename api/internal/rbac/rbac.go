// Package rbac centralizes authorization decisions. No domain package should
// implement its own ad-hoc "is admin" boolean check (rule 5) — everything
// goes through Check or authctx.AuthContext.HasPermission, which Check uses
// to resolve the permission set in the first place.
package rbac

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/platform/logger"
)

type AuditRecorder interface {
	Record(ctx context.Context, ac authctx.AuthContext, e audit.Entry) error
}

type Service struct {
	pool  *pgxpool.Pool
	audit AuditRecorder
}

func New(pool *pgxpool.Pool, auditRecorder AuditRecorder) *Service {
	return &Service{pool: pool, audit: auditRecorder}
}

// ResolvePermissions computes the full set of permission keys a user holds
// within an organization, by unioning the permissions of every role granted
// to them there. This is called once per request (by the identity module,
// when it builds the AuthContext) rather than on every permission check.
func (s *Service) ResolvePermissions(ctx context.Context, orgID, userID uuid.UUID) (map[string]struct{}, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT rp.permission_key
		FROM organization_member_roles omr
		JOIN role_permissions rp ON rp.role_id = omr.role_id
		WHERE omr.organization_id = $1 AND omr.user_id = $2
	`, orgID, userID)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to resolve permissions", err)
	}
	defer rows.Close()

	perms := make(map[string]struct{})
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan permission", err)
		}
		perms[key] = struct{}{}
	}
	return perms, rows.Err()
}

// Role is a role available within an organization — either system-defined
// (available to every org) or, once custom org-defined roles exist, scoped
// to one org. Not yet creatable through the API (docs/ROADMAP.md); the
// three seeded system roles (owner/admin/member, migration 0002) are all
// that exist today.
type Role struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	IsSystem    bool      `json:"is_system"`
	Permissions []string  `json:"permissions"`
}

// ListRoles returns every role available to the calling organization:
// every system role (organization_id IS NULL) plus any custom role
// defined specifically for this org.
func (s *Service) ListRoles(ctx context.Context, ac authctx.AuthContext) ([]Role, error) {
	if err := Require(ac, "organization.manage"); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.name, r.description, r.is_system, COALESCE(rp.permission_key, '')
		FROM roles r
		LEFT JOIN role_permissions rp ON rp.role_id = r.id
		WHERE r.organization_id IS NULL OR r.organization_id = $1
		ORDER BY r.name ASC, rp.permission_key ASC
	`, ac.OrganizationID)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list roles", err)
	}
	defer rows.Close()

	index := make(map[uuid.UUID]int)
	var out []Role
	for rows.Next() {
		var id uuid.UUID
		var name, description, permKey string
		var isSystem bool
		if err := rows.Scan(&id, &name, &description, &isSystem, &permKey); err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan role", err)
		}
		idx, ok := index[id]
		if !ok {
			out = append(out, Role{ID: id, Name: name, Description: description, IsSystem: isSystem, Permissions: []string{}})
			idx = len(out) - 1
			index[id] = idx
		}
		if permKey != "" {
			out[idx].Permissions = append(out[idx].Permissions, permKey)
		}
	}
	return out, rows.Err()
}

// MemberRole is one role held by a member, as returned by ListMembers.
type MemberRole struct {
	RoleID uuid.UUID `json:"role_id"`
	Name   string    `json:"name"`
}

// Member is one user's membership in the calling organization, with every
// role they currently hold there.
type Member struct {
	UserID      uuid.UUID    `json:"user_id"`
	Email       string       `json:"email"`
	DisplayName string       `json:"display_name"`
	Roles       []MemberRole `json:"roles"`
}

// ListMembers returns every member of the calling organization along with
// their currently-assigned roles. A member with no role assigned still
// appears, with an empty Roles slice — that's a legitimate (if inert)
// state, not an error.
func (s *Service) ListMembers(ctx context.Context, ac authctx.AuthContext) ([]Member, error) {
	if err := Require(ac, "organization.manage"); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT u.id, u.email, u.display_name, r.id, r.name
		FROM organization_members om
		JOIN users u ON u.id = om.user_id
		LEFT JOIN organization_member_roles omr ON omr.organization_id = om.organization_id AND omr.user_id = om.user_id
		LEFT JOIN roles r ON r.id = omr.role_id
		WHERE om.organization_id = $1
		ORDER BY u.email ASC, r.name ASC
	`, ac.OrganizationID)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list organization members", err)
	}
	defer rows.Close()

	index := make(map[uuid.UUID]int)
	var out []Member
	for rows.Next() {
		var userID uuid.UUID
		var email, displayName string
		var roleID *uuid.UUID
		var roleName *string
		if err := rows.Scan(&userID, &email, &displayName, &roleID, &roleName); err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan organization member", err)
		}
		idx, ok := index[userID]
		if !ok {
			out = append(out, Member{UserID: userID, Email: email, DisplayName: displayName, Roles: []MemberRole{}})
			idx = len(out) - 1
			index[userID] = idx
		}
		if roleID != nil {
			out[idx].Roles = append(out[idx].Roles, MemberRole{RoleID: *roleID, Name: *roleName})
		}
	}
	return out, rows.Err()
}

// AssignRole grants userID a role within the calling organization.
// Idempotent — assigning a role the member already holds is not an error.
func (s *Service) AssignRole(ctx context.Context, ac authctx.AuthContext, userID, roleID uuid.UUID) error {
	if err := Require(ac, "organization.manage"); err != nil {
		return err
	}

	var isMember bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM organization_members WHERE organization_id = $1 AND user_id = $2)
	`, ac.OrganizationID, userID).Scan(&isMember); err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to check organization membership", err)
	}
	if !isMember {
		return apierr.NotFound("organization member")
	}

	var roleAvailable bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM roles WHERE id = $1 AND (organization_id IS NULL OR organization_id = $2))
	`, roleID, ac.OrganizationID).Scan(&roleAvailable); err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to check role", err)
	}
	if !roleAvailable {
		return apierr.NotFound("role")
	}

	if _, err := s.pool.Exec(ctx, `
		INSERT INTO organization_member_roles (organization_id, user_id, role_id) VALUES ($1, $2, $3)
		ON CONFLICT (organization_id, user_id, role_id) DO NOTHING
	`, ac.OrganizationID, userID, roleID); err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to assign role", err)
	}

	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: "rbac.role.assigned", ResourceType: "organization_member", ResourceID: userID.String(),
		Success: true, Metadata: map[string]any{"role_id": roleID.String()},
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}
	return nil
}

// RevokeRole removes a role from userID within the calling organization.
func (s *Service) RevokeRole(ctx context.Context, ac authctx.AuthContext, userID, roleID uuid.UUID) error {
	if err := Require(ac, "organization.manage"); err != nil {
		return err
	}

	tag, err := s.pool.Exec(ctx, `
		DELETE FROM organization_member_roles WHERE organization_id = $1 AND user_id = $2 AND role_id = $3
	`, ac.OrganizationID, userID, roleID)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to revoke role", err)
	}
	if tag.RowsAffected() == 0 {
		return apierr.NotFound("role assignment")
	}

	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: "rbac.role.revoked", ResourceType: "organization_member", ResourceID: userID.String(),
		Success: true, Metadata: map[string]any{"role_id": roleID.String()},
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}
	return nil
}

// Require returns a *apierr.Error (CodeForbidden) unless ac holds permission,
// or ac is a system actor (authctx.ActorSystem), which bypasses checks
// entirely because it is never derived from an untrusted client request.
//
// Every domain service method that performs a sensitive read or any mutation
// must call Require before touching data (docs/SECURITY.md).
func Require(ac authctx.AuthContext, permission string) error {
	if ac.ActorType == authctx.ActorSystem {
		return nil
	}
	if !ac.HasPermission(permission) {
		return apierr.Forbidden(fmt.Sprintf("missing required permission: %s", permission))
	}
	return nil
}
