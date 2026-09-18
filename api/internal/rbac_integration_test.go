package integration_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/testhelpers"
)

// systemOwnerRoleID / systemAdminRoleID mirror systemMemberRoleID
// (harness_test.go) — the seeded system roles from migration 0002_rbac.sql.
const (
	systemOwnerRoleIDStr = "00000000-0000-0000-0000-000000000001"
	systemAdminRoleIDStr = "00000000-0000-0000-0000-000000000002"
)

func mustParseUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("invalid UUID %q: %v", s, err)
	}
	return id
}

// ListRoles returns every system role (available to every org) with its
// full permission set.
func TestRBAC_ListRolesReturnsSystemRoles(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "rbac-list-roles-owner@nodera.dev")

	roles, err := h.rbac.ListRoles(ctx, ac)
	if err != nil {
		t.Fatalf("ListRoles: %v", err)
	}
	if len(roles) != 3 {
		t.Fatalf("expected the 3 seeded system roles (owner/admin/member), got %d: %+v", len(roles), roles)
	}
	for _, r := range roles {
		if !r.IsSystem {
			t.Fatalf("expected every role to be a system role (no custom org roles exist yet), got %+v", r)
		}
		if r.Name == "owner" && len(r.Permissions) == 0 {
			t.Fatal("expected the owner role to carry every permission, got none")
		}
	}
}

// A plain member lacks organization.manage, so ListRoles is forbidden for
// them — the roles/permissions catalog is settings-page-sensitive info.
func TestRBAC_ListRolesRequiresOrganizationManage(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "rbac-perm-owner@nodera.dev")
	memberAC := h.newMemberContext(t, ctx, ac.OrganizationID, "rbac-perm-member@nodera.dev")

	if _, err := h.rbac.ListRoles(ctx, memberAC); err == nil {
		t.Fatal("expected a plain member to be forbidden from listing roles")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}
}

// ListMembers reflects every member of the org and their current roles —
// the owner who created the org already holds the owner role from
// CreateOrganization, and a freshly-added member with no role yet shows an
// empty Roles slice rather than being omitted.
func TestRBAC_ListMembersReflectsCurrentRoles(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "rbac-members-owner@nodera.dev")
	_ = h.newMemberContext(t, ctx, ac.OrganizationID, "rbac-members-member@nodera.dev")

	members, err := h.rbac.ListMembers(ctx, ac)
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 members (owner + the added member), got %d: %+v", len(members), members)
	}

	var ownerFound, memberFound bool
	for _, m := range members {
		switch m.Email {
		case "rbac-members-owner@nodera.dev":
			ownerFound = true
			if len(m.Roles) != 1 || m.Roles[0].Name != "owner" {
				t.Fatalf("expected the creator to hold exactly the owner role, got %+v", m.Roles)
			}
		case "rbac-members-member@nodera.dev":
			memberFound = true
			if len(m.Roles) != 1 || m.Roles[0].Name != "member" {
				t.Fatalf("expected the added member to hold exactly the member role, got %+v", m.Roles)
			}
		}
	}
	if !ownerFound || !memberFound {
		t.Fatalf("expected both members to be present, got %+v", members)
	}
}

// AssignRole grants an additional role to an existing member, and
// RevokeRole removes it again — both idempotent/tolerant in the expected
// ways (assigning an already-held role is a no-op, not an error).
func TestRBAC_AssignAndRevokeRole(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "rbac-assign-owner@nodera.dev")
	memberAC := h.newMemberContext(t, ctx, ac.OrganizationID, "rbac-assign-member@nodera.dev")

	adminRoleID := mustParseUUID(t, systemAdminRoleIDStr)

	if err := h.rbac.AssignRole(ctx, ac, memberAC.ActorID, adminRoleID); err != nil {
		t.Fatalf("AssignRole: %v", err)
	}
	// Assigning the same role again is a no-op, not an error.
	if err := h.rbac.AssignRole(ctx, ac, memberAC.ActorID, adminRoleID); err != nil {
		t.Fatalf("AssignRole (repeat): %v", err)
	}

	members, err := h.rbac.ListMembers(ctx, ac)
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	var got []string
	for _, m := range members {
		if m.Email == "rbac-assign-member@nodera.dev" {
			for _, r := range m.Roles {
				got = append(got, r.Name)
			}
		}
	}
	if len(got) != 2 {
		t.Fatalf("expected the member to now hold both member and admin roles, got %+v", got)
	}

	if err := h.rbac.RevokeRole(ctx, ac, memberAC.ActorID, adminRoleID); err != nil {
		t.Fatalf("RevokeRole: %v", err)
	}
	membersAfter, err := h.rbac.ListMembers(ctx, ac)
	if err != nil {
		t.Fatalf("ListMembers (after revoke): %v", err)
	}
	for _, m := range membersAfter {
		if m.Email == "rbac-assign-member@nodera.dev" {
			if len(m.Roles) != 1 || m.Roles[0].Name != "member" {
				t.Fatalf("expected the admin role to be revoked, leaving only member, got %+v", m.Roles)
			}
		}
	}

	// Revoking a role assignment that no longer exists is a NOT_FOUND, not
	// silently ignored — the caller should be told nothing happened.
	if err := h.rbac.RevokeRole(ctx, ac, memberAC.ActorID, adminRoleID); err == nil {
		t.Fatal("expected revoking an already-revoked role to fail")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeNotFound {
		t.Fatalf("expected NOT_FOUND, got %v", err)
	}
}

// AssignRole refuses a role_id from another organization's custom role
// space and a user_id that isn't actually a member of the calling org.
func TestRBAC_AssignRoleRejectsInvalidTargets(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "rbac-invalid-owner@nodera.dev")

	adminRoleID := mustParseUUID(t, systemAdminRoleIDStr)
	randomUserID := mustParseUUID(t, "11111111-1111-1111-1111-111111111111")

	if err := h.rbac.AssignRole(ctx, ac, randomUserID, adminRoleID); err == nil {
		t.Fatal("expected assigning a role to a non-member to fail")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeNotFound {
		t.Fatalf("expected NOT_FOUND for a non-member user, got %v", err)
	}

	randomRoleID := mustParseUUID(t, "22222222-2222-2222-2222-222222222222")
	if err := h.rbac.AssignRole(ctx, ac, ac.ActorID, randomRoleID); err == nil {
		t.Fatal("expected assigning an unknown role to fail")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeNotFound {
		t.Fatalf("expected NOT_FOUND for an unknown role, got %v", err)
	}
}

// CreateRole defines a new org-scoped custom role and it becomes
// immediately assignable, appearing in ListRoles/ListMembers exactly like
// a system role once granted.
func TestRBAC_CreateRoleAndAssignIt(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "rbac-createrole-owner@nodera.dev")
	memberAC := h.newMemberContext(t, ctx, ac.OrganizationID, "rbac-createrole-member@nodera.dev")

	role, err := h.rbac.CreateRole(ctx, ac, "auditor", "Read-only audit access", []string{"audit.read", "infrastructure.read"})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if role.IsSystem {
		t.Fatal("expected a custom role to not be marked is_system")
	}
	if len(role.Permissions) != 2 {
		t.Fatalf("expected 2 permissions, got %+v", role.Permissions)
	}

	roles, err := h.rbac.ListRoles(ctx, ac)
	if err != nil {
		t.Fatalf("ListRoles: %v", err)
	}
	if len(roles) != 4 {
		t.Fatalf("expected 3 system roles + 1 custom role, got %d: %+v", len(roles), roles)
	}

	if err := h.rbac.AssignRole(ctx, ac, memberAC.ActorID, role.ID); err != nil {
		t.Fatalf("AssignRole (custom role): %v", err)
	}
	members, err := h.rbac.ListMembers(ctx, ac)
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	for _, m := range members {
		if m.Email == "rbac-createrole-member@nodera.dev" {
			var names []string
			for _, r := range m.Roles {
				names = append(names, r.Name)
			}
			if len(names) != 2 {
				t.Fatalf("expected the member to hold both member and auditor, got %+v", names)
			}
		}
	}
}

// CreateRole enforces the same no-privilege-escalation rule as API token
// scopes and agent permission_scope: a caller can't grant a permission it
// doesn't itself hold, and every permission key must be real.
func TestRBAC_CreateRoleRejectsInvalidPermissions(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "rbac-createrole-invalid-owner@nodera.dev")
	memberAC := h.newMemberContext(t, ctx, ac.OrganizationID, "rbac-createrole-invalid-member@nodera.dev")

	if _, err := h.rbac.CreateRole(ctx, memberAC, "escalated", "", []string{"organization.manage"}); err == nil {
		t.Fatal("expected a member to be forbidden from creating a role with a permission they don't hold")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}

	if _, err := h.rbac.CreateRole(ctx, ac, "bad-perm-role", "", []string{"not.a.real.permission"}); err == nil {
		t.Fatal("expected creating a role with an unknown permission key to fail")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeValidation {
		t.Fatalf("expected VALIDATION_ERROR, got %v", err)
	}
}

// UpdateRolePermissions replaces a custom role's entire permission set,
// and refuses to touch a system role (getCustomRole excludes them, so the
// same "role not found" error a nonexistent ID would produce).
func TestRBAC_UpdateRolePermissions(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "rbac-updaterole-owner@nodera.dev")

	role, err := h.rbac.CreateRole(ctx, ac, "limited", "", []string{"audit.read"})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	updated, err := h.rbac.UpdateRolePermissions(ctx, ac, role.ID, []string{"infrastructure.read", "jobs.read"})
	if err != nil {
		t.Fatalf("UpdateRolePermissions: %v", err)
	}
	if len(updated.Permissions) != 2 {
		t.Fatalf("expected the permission set to be fully replaced with 2 entries, got %+v", updated.Permissions)
	}

	systemAdminRoleID := mustParseUUID(t, systemAdminRoleIDStr)
	if _, err := h.rbac.UpdateRolePermissions(ctx, ac, systemAdminRoleID, []string{"audit.read"}); err == nil {
		t.Fatal("expected updating a system role's permissions to fail")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeNotFound {
		t.Fatalf("expected NOT_FOUND for a system role, got %v", err)
	}
}

// DeleteRole refuses to remove a role still assigned to a member — the
// caller must revoke it first, so deletion never silently changes what a
// member can do.
func TestRBAC_DeleteRoleRequiresNoAssignments(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "rbac-deleterole-owner@nodera.dev")
	memberAC := h.newMemberContext(t, ctx, ac.OrganizationID, "rbac-deleterole-member@nodera.dev")

	role, err := h.rbac.CreateRole(ctx, ac, "temp-role", "", []string{"audit.read"})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if err := h.rbac.AssignRole(ctx, ac, memberAC.ActorID, role.ID); err != nil {
		t.Fatalf("AssignRole: %v", err)
	}

	if err := h.rbac.DeleteRole(ctx, ac, role.ID); err == nil {
		t.Fatal("expected deleting an assigned role to fail")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeConflict {
		t.Fatalf("expected CONFLICT, got %v", err)
	}

	if err := h.rbac.RevokeRole(ctx, ac, memberAC.ActorID, role.ID); err != nil {
		t.Fatalf("RevokeRole: %v", err)
	}
	if err := h.rbac.DeleteRole(ctx, ac, role.ID); err != nil {
		t.Fatalf("DeleteRole (after revoking): %v", err)
	}

	roles, err := h.rbac.ListRoles(ctx, ac)
	if err != nil {
		t.Fatalf("ListRoles: %v", err)
	}
	for _, r := range roles {
		if r.ID == role.ID {
			t.Fatalf("expected the deleted role to no longer be listed, got %+v", roles)
		}
	}
}

// UpdateRoleDetails renames a custom role and/or changes its description
// without touching its permission set, and refuses a system role with the
// same NOT_FOUND getCustomRole already produces elsewhere.
func TestRBAC_UpdateRoleDetails(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "rbac-updatedetails-owner@nodera.dev")

	role, err := h.rbac.CreateRole(ctx, ac, "old-name", "old description", []string{"audit.read"})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	updated, err := h.rbac.UpdateRoleDetails(ctx, ac, role.ID, "new-name", "new description")
	if err != nil {
		t.Fatalf("UpdateRoleDetails: %v", err)
	}
	if updated.Name != "new-name" || updated.Description != "new description" {
		t.Fatalf("expected name/description to be updated, got %+v", updated)
	}
	if len(updated.Permissions) != 1 || updated.Permissions[0] != "audit.read" {
		t.Fatalf("expected the permission set to be untouched by a details-only update, got %+v", updated.Permissions)
	}

	systemAdminRoleID := mustParseUUID(t, systemAdminRoleIDStr)
	if _, err := h.rbac.UpdateRoleDetails(ctx, ac, systemAdminRoleID, "hijacked", ""); err == nil {
		t.Fatal("expected renaming a system role to fail")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeNotFound {
		t.Fatalf("expected NOT_FOUND for a system role, got %v", err)
	}
}
