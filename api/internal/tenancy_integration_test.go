package integration_test

import (
	"context"
	"testing"

	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/testhelpers"
)

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
