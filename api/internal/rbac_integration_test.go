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
