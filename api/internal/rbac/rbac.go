// Package rbac centralizes authorization decisions. No domain package should
// implement its own ad-hoc "is admin" boolean check (rule 5) — everything
// goes through Check or authctx.AuthContext.HasPermission, which Check uses
// to resolve the permission set in the first place.
package rbac

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

// validatePermissionSubset returns apierr.Forbidden unless every key in
// permissions is one ac itself holds — the same no-privilege-escalation
// rule applied to API token scopes (internal/identity) and agent
// permission_scope (internal/agents): nobody can grant a role broader
// power than they themselves have.
func validatePermissionSubset(ac authctx.AuthContext, permissions []string) error {
	for _, p := range permissions {
		if !ac.HasPermission(p) {
			return apierr.Forbidden("cannot grant permission you do not hold: " + p)
		}
	}
	return nil
}

// validatePermissionKeys returns apierr.Validation if any key in
// permissions doesn't exist in the permissions catalog.
func (s *Service) validatePermissionKeys(ctx context.Context, permissions []string) error {
	for _, p := range permissions {
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM permissions WHERE key = $1)`, p).Scan(&exists); err != nil {
			return apierr.Wrap(apierr.CodeInternal, "failed to validate permission key", err)
		}
		if !exists {
			return apierr.Validation("unknown permission key: " + p)
		}
	}
	return nil
}

// CreateRole defines a new custom role scoped to the calling organization
// (roles.organization_id) — distinct from the 3 seeded system roles
// (owner/admin/member), which remain fixed. permissions must be a subset
// of the caller's own held permissions (no privilege escalation) and must
// each be a real key in the permissions catalog.
func (s *Service) CreateRole(ctx context.Context, ac authctx.AuthContext, name, description string, permissions []string) (Role, error) {
	if err := Require(ac, "organization.manage"); err != nil {
		return Role{}, err
	}
	if name == "" {
		return Role{}, apierr.Validation("role name is required")
	}
	if err := s.validatePermissionKeys(ctx, permissions); err != nil {
		return Role{}, err
	}
	if err := validatePermissionSubset(ac, permissions); err != nil {
		return Role{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Role{}, apierr.Wrap(apierr.CodeInternal, "failed to begin transaction", err)
	}
	defer tx.Rollback(ctx)

	var r Role
	err = tx.QueryRow(ctx, `
		INSERT INTO roles (organization_id, name, description, is_system) VALUES ($1, $2, $3, false)
		RETURNING id, name, description, is_system
	`, ac.OrganizationID, name, description).Scan(&r.ID, &r.Name, &r.Description, &r.IsSystem)
	if err != nil {
		if isUniqueViolation(err) {
			return Role{}, apierr.Conflict("a role with this name already exists in this organization")
		}
		return Role{}, apierr.Wrap(apierr.CodeInternal, "failed to create role", err)
	}

	for _, p := range permissions {
		if _, err := tx.Exec(ctx, `
			INSERT INTO role_permissions (role_id, permission_key) VALUES ($1, $2)
		`, r.ID, p); err != nil {
			return Role{}, apierr.Wrap(apierr.CodeInternal, "failed to grant permission to role", err)
		}
	}
	r.Permissions = permissions
	if r.Permissions == nil {
		r.Permissions = []string{}
	}

	if err := tx.Commit(ctx); err != nil {
		return Role{}, apierr.Wrap(apierr.CodeInternal, "failed to commit transaction", err)
	}

	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: "rbac.role.created", ResourceType: "role", ResourceID: r.ID.String(),
		Success: true, ResultingState: r,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}

	return r, nil
}

// getCustomRole loads a role by ID, scoped to the calling org, and refuses
// (NotFound) if it's a system role or belongs to a different org — every
// mutation below (UpdateRolePermissions, DeleteRole) starts here so a
// system role's fixed permission set can never be edited through this
// path, only through a migration (docs/AGENTS.md-style deliberate
// friction for a sensitive, rarely-changed catalog).
func (s *Service) getCustomRole(ctx context.Context, orgID, roleID uuid.UUID) (Role, error) {
	var r Role
	var dbOrgID *uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT id, organization_id, name, description, is_system FROM roles WHERE id = $1
	`, roleID).Scan(&r.ID, &dbOrgID, &r.Name, &r.Description, &r.IsSystem)
	if errors.Is(err, pgx.ErrNoRows) {
		return Role{}, apierr.NotFound("role")
	}
	if err != nil {
		return Role{}, apierr.Wrap(apierr.CodeInternal, "failed to load role", err)
	}
	if r.IsSystem || dbOrgID == nil || *dbOrgID != orgID {
		return Role{}, apierr.NotFound("role")
	}
	return r, nil
}

// loadRolePermissions returns a role's current permission set, used by
// UpdateRoleDetails to fill in the Permissions field of its response (the
// UPDATE itself only touches name/description, so the permission set has
// to be read back separately rather than assumed from the request).
func (s *Service) loadRolePermissions(ctx context.Context, roleID uuid.UUID) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT permission_key FROM role_permissions WHERE role_id = $1 ORDER BY permission_key ASC
	`, roleID)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to load role permissions", err)
	}
	defer rows.Close()

	perms := []string{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan role permission", err)
		}
		perms = append(perms, p)
	}
	return perms, rows.Err()
}

// UpdateRoleDetails renames a custom role and/or changes its description.
// Its permission set is untouched — see UpdateRolePermissions for that.
func (s *Service) UpdateRoleDetails(ctx context.Context, ac authctx.AuthContext, roleID uuid.UUID, name, description string) (Role, error) {
	if err := Require(ac, "organization.manage"); err != nil {
		return Role{}, err
	}
	if name == "" {
		return Role{}, apierr.Validation("role name is required")
	}
	if _, err := s.getCustomRole(ctx, ac.OrganizationID, roleID); err != nil {
		return Role{}, err
	}

	var r Role
	err := s.pool.QueryRow(ctx, `
		UPDATE roles SET name = $2, description = $3 WHERE id = $1
		RETURNING id, name, description, is_system
	`, roleID, name, description).Scan(&r.ID, &r.Name, &r.Description, &r.IsSystem)
	if err != nil {
		if isUniqueViolation(err) {
			return Role{}, apierr.Conflict("a role with this name already exists in this organization")
		}
		return Role{}, apierr.Wrap(apierr.CodeInternal, "failed to update role", err)
	}

	perms, err := s.loadRolePermissions(ctx, roleID)
	if err != nil {
		return Role{}, err
	}
	r.Permissions = perms

	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: "rbac.role.details_updated", ResourceType: "role", ResourceID: r.ID.String(),
		Success: true, ResultingState: r,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}

	return r, nil
}

// UpdateRolePermissions replaces a custom role's entire permission set.
// The new set, like CreateRole's, must be a subset of the caller's own
// held permissions.
func (s *Service) UpdateRolePermissions(ctx context.Context, ac authctx.AuthContext, roleID uuid.UUID, permissions []string) (Role, error) {
	if err := Require(ac, "organization.manage"); err != nil {
		return Role{}, err
	}
	r, err := s.getCustomRole(ctx, ac.OrganizationID, roleID)
	if err != nil {
		return Role{}, err
	}
	if err := s.validatePermissionKeys(ctx, permissions); err != nil {
		return Role{}, err
	}
	if err := validatePermissionSubset(ac, permissions); err != nil {
		return Role{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Role{}, apierr.Wrap(apierr.CodeInternal, "failed to begin transaction", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM role_permissions WHERE role_id = $1`, roleID); err != nil {
		return Role{}, apierr.Wrap(apierr.CodeInternal, "failed to clear existing permissions", err)
	}
	for _, p := range permissions {
		if _, err := tx.Exec(ctx, `
			INSERT INTO role_permissions (role_id, permission_key) VALUES ($1, $2)
		`, roleID, p); err != nil {
			return Role{}, apierr.Wrap(apierr.CodeInternal, "failed to grant permission to role", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Role{}, apierr.Wrap(apierr.CodeInternal, "failed to commit transaction", err)
	}

	r.Permissions = permissions
	if r.Permissions == nil {
		r.Permissions = []string{}
	}

	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: "rbac.role.permissions_updated", ResourceType: "role", ResourceID: r.ID.String(),
		Success: true, ResultingState: r,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}

	return r, nil
}

// DeleteRole removes a custom role, refusing (Conflict) if any member
// currently holds it — the caller must revoke every assignment first,
// rather than this silently changing what a member can do as a side
// effect of an unrelated cleanup action.
func (s *Service) DeleteRole(ctx context.Context, ac authctx.AuthContext, roleID uuid.UUID) error {
	if err := Require(ac, "organization.manage"); err != nil {
		return err
	}
	if _, err := s.getCustomRole(ctx, ac.OrganizationID, roleID); err != nil {
		return err
	}

	var assignedCount int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM organization_member_roles WHERE role_id = $1
	`, roleID).Scan(&assignedCount); err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to check role assignments", err)
	}
	if assignedCount > 0 {
		return apierr.Conflict("role is still assigned to one or more members; revoke it from them first")
	}

	if _, err := s.pool.Exec(ctx, `DELETE FROM roles WHERE id = $1`, roleID); err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to delete role", err)
	}

	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: "rbac.role.deleted", ResourceType: "role", ResourceID: roleID.String(),
		Success: true,
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

func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23505"
	}
	return false
}
