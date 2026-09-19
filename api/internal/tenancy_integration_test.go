package integration_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/testhelpers"
)

// systemOwnerRoleID mirrors the unexported constant in internal/tenancy —
// the seeded 'owner' system role from migration 0002_rbac.sql.
var systemOwnerRoleID = uuid.MustParse("00000000-0000-0000-0000-000000000001")

// AddMember joins an already-existing user (signed up but not yet part of
// any organization) to the calling org, granting them the system 'member'
// role — the same starting point every org's creator gets for themselves,
// minus 'owner'.
func TestTenancy_AddMemberGrantsMemberRole(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "addmember-owner@nodera.dev")

	if _, err := h.identity.SignUp(ctx, "addmember-invitee@nodera.dev", "correct horse battery staple 9", "Invitee"); err != nil {
		t.Fatalf("SignUp: %v", err)
	}

	added, err := h.tenancy.AddMember(ctx, ac, "addmember-invitee@nodera.dev")
	if err != nil {
		t.Fatalf("AddMember: %v", err)
	}
	if added.Email != "addmember-invitee@nodera.dev" {
		t.Fatalf("unexpected email on the returned member: %q", added.Email)
	}

	members, err := h.rbac.ListMembers(ctx, ac)
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	var found bool
	for _, m := range members {
		if m.Email == "addmember-invitee@nodera.dev" {
			found = true
			if len(m.Roles) != 1 || m.Roles[0].Name != "member" {
				t.Fatalf("expected the new member to hold exactly the member role, got %+v", m.Roles)
			}
		}
	}
	if !found {
		t.Fatalf("expected the invitee to appear in ListMembers, got %+v", members)
	}
}

// A plain member lacks organization.manage, so they can't add anyone —
// same gate as ListRoles/ListMembers/AssignRole.
func TestTenancy_AddMemberRequiresOrganizationManage(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "addmember-perm-owner@nodera.dev")
	memberAC := h.newMemberContext(t, ctx, ac.OrganizationID, "addmember-perm-member@nodera.dev")

	if _, err := h.identity.SignUp(ctx, "addmember-perm-invitee@nodera.dev", "correct horse battery staple 9", "Invitee"); err != nil {
		t.Fatalf("SignUp: %v", err)
	}

	if _, err := h.tenancy.AddMember(ctx, memberAC, "addmember-perm-invitee@nodera.dev"); err == nil {
		t.Fatal("expected a plain member to be forbidden from adding a member")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}
}

// AddMember reports NOT_FOUND for an email with no account — it never
// creates one (that's SignUp's job).
func TestTenancy_AddMemberRejectsUnknownEmail(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "addmember-unknown-owner@nodera.dev")

	if _, err := h.tenancy.AddMember(ctx, ac, "nobody-signed-up-with-this@nodera.dev"); err == nil {
		t.Fatal("expected adding an unknown email to fail")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeNotFound {
		t.Fatalf("expected NOT_FOUND, got %v", err)
	}
}

// Adding a user who is already a member of the org is a CONFLICT, not a
// silent no-op — the caller asked to add someone, and nothing changed.
func TestTenancy_AddMemberRejectsAlreadyMember(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "addmember-dup-owner@nodera.dev")

	if _, err := h.identity.SignUp(ctx, "addmember-dup-invitee@nodera.dev", "correct horse battery staple 9", "Invitee"); err != nil {
		t.Fatalf("SignUp: %v", err)
	}
	if _, err := h.tenancy.AddMember(ctx, ac, "addmember-dup-invitee@nodera.dev"); err != nil {
		t.Fatalf("AddMember (first): %v", err)
	}
	if _, err := h.tenancy.AddMember(ctx, ac, "addmember-dup-invitee@nodera.dev"); err == nil {
		t.Fatal("expected adding an already-added member to fail")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeConflict {
		t.Fatalf("expected CONFLICT, got %v", err)
	}
}

// RemoveMember drops both the membership row and, via the composite FK's
// cascade, every role grant the member held — confirmed by re-adding the
// same person afterward, which only succeeds if no stale membership row
// remains.
func TestTenancy_RemoveMemberDropsMembershipAndRoles(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "removemember-owner@nodera.dev")

	if _, err := h.identity.SignUp(ctx, "removemember-invitee@nodera.dev", "correct horse battery staple 9", "Invitee"); err != nil {
		t.Fatalf("SignUp: %v", err)
	}
	added, err := h.tenancy.AddMember(ctx, ac, "removemember-invitee@nodera.dev")
	if err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	if err := h.tenancy.RemoveMember(ctx, ac, added.UserID); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}

	members, err := h.rbac.ListMembers(ctx, ac)
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	for _, m := range members {
		if m.Email == "removemember-invitee@nodera.dev" {
			t.Fatalf("expected the removed member to no longer appear, got %+v", m)
		}
	}

	// Re-adding must succeed cleanly — a leftover organization_members or
	// organization_member_roles row would surface as a CONFLICT here.
	if _, err := h.tenancy.AddMember(ctx, ac, "removemember-invitee@nodera.dev"); err != nil {
		t.Fatalf("AddMember (after remove): expected a clean re-add to succeed, got %v", err)
	}
}

func TestTenancy_RemoveMemberRequiresOrganizationManage(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "removemember-perm-owner@nodera.dev")
	memberAC := h.newMemberContext(t, ctx, ac.OrganizationID, "removemember-perm-member@nodera.dev")

	if _, err := h.identity.SignUp(ctx, "removemember-perm-victim@nodera.dev", "correct horse battery staple 9", "Victim"); err != nil {
		t.Fatalf("SignUp: %v", err)
	}
	added, err := h.tenancy.AddMember(ctx, ac, "removemember-perm-victim@nodera.dev")
	if err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	if err := h.tenancy.RemoveMember(ctx, memberAC, added.UserID); err == nil {
		t.Fatal("expected a plain member to be forbidden from removing a member")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}
}

func TestTenancy_RemoveMemberNotFoundForNonMember(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "removemember-notfound-owner@nodera.dev")

	if err := h.tenancy.RemoveMember(ctx, ac, uuid.New()); err == nil {
		t.Fatal("expected removing a nonexistent member to fail")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeNotFound {
		t.Fatalf("expected NOT_FOUND, got %v", err)
	}
}

// The sole owner can never remove themselves (or be removed) — that would
// leave the organization with no one able to manage it.
func TestTenancy_RemoveMemberRefusesLastOwner(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "removemember-lastowner-owner@nodera.dev")

	if err := h.tenancy.RemoveMember(ctx, ac, ac.ActorID); err == nil {
		t.Fatal("expected removing the last owner to fail")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeConflict {
		t.Fatalf("expected CONFLICT, got %v", err)
	}
}

// With a second owner in place, removing the first succeeds — the guard
// is specifically about the *last* owner, not owners in general.
func TestTenancy_RemoveMemberAllowsRemovingOwnerWhenAnotherRemains(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "removemember-secondowner-owner@nodera.dev")

	if _, err := h.identity.SignUp(ctx, "removemember-secondowner-co@nodera.dev", "correct horse battery staple 9", "Co-owner"); err != nil {
		t.Fatalf("SignUp: %v", err)
	}
	added, err := h.tenancy.AddMember(ctx, ac, "removemember-secondowner-co@nodera.dev")
	if err != nil {
		t.Fatalf("AddMember: %v", err)
	}
	if err := h.rbac.AssignRole(ctx, ac, added.UserID, systemOwnerRoleID); err != nil {
		t.Fatalf("AssignRole (owner): %v", err)
	}

	if err := h.tenancy.RemoveMember(ctx, ac, ac.ActorID); err != nil {
		t.Fatalf("expected removing the first owner to succeed with a second owner present, got %v", err)
	}
}
