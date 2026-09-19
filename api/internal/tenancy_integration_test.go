package integration_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/tenancy"
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

func TestTenancy_UpdateOrganizationChangesOnlyProvidedFields(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "updateorg-owner@nodera.dev")

	before, err := h.tenancy.Get(ctx, ac)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	newName := "Renamed Org"
	updated, err := h.tenancy.UpdateOrganization(ctx, ac, tenancy.UpdateOrganizationInput{Name: &newName})
	if err != nil {
		t.Fatalf("UpdateOrganization: %v", err)
	}
	if updated.Name != "Renamed Org" {
		t.Fatalf("expected name to be updated, got %q", updated.Name)
	}
	if updated.Slug != before.Slug {
		t.Fatalf("untouched field Slug changed: got %q, want %q", updated.Slug, before.Slug)
	}

	newSlug := before.Slug + "-v2"
	updated, err = h.tenancy.UpdateOrganization(ctx, ac, tenancy.UpdateOrganizationInput{Slug: &newSlug})
	if err != nil {
		t.Fatalf("UpdateOrganization (slug): %v", err)
	}
	if updated.Slug != newSlug {
		t.Fatalf("expected slug to be updated, got %q", updated.Slug)
	}
	if updated.Name != "Renamed Org" {
		t.Fatalf("untouched field Name changed on the second call: got %q", updated.Name)
	}
}

func TestTenancy_UpdateOrganizationRequiresOrganizationManage(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "updateorg-perm-owner@nodera.dev")
	memberAC := h.newMemberContext(t, ctx, ac.OrganizationID, "updateorg-perm-member@nodera.dev")

	newName := "should-not-apply"
	if _, err := h.tenancy.UpdateOrganization(ctx, memberAC, tenancy.UpdateOrganizationInput{Name: &newName}); err == nil {
		t.Fatal("expected a plain member to be forbidden from updating the organization")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}
}

func TestTenancy_UpdateOrganizationRejectsInvalidSlug(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "updateorg-invalid-owner@nodera.dev")

	badSlug := "Not A Valid Slug!"
	if _, err := h.tenancy.UpdateOrganization(ctx, ac, tenancy.UpdateOrganizationInput{Slug: &badSlug}); err == nil {
		t.Fatal("expected an invalid slug to fail validation")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeValidation {
		t.Fatalf("expected VALIDATION_ERROR, got %v", err)
	}
}

// Renaming to another organization's slug is a real CONFLICT, since slugs
// are globally unique.
func TestTenancy_UpdateOrganizationRejectsDuplicateSlug(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	acA, _ := h.newOwnerContext(t, ctx, "updateorg-dup-a@nodera.dev")
	acB, _ := h.newOwnerContext(t, ctx, "updateorg-dup-b@nodera.dev")

	orgA, err := h.tenancy.Get(ctx, acA)
	if err != nil {
		t.Fatalf("Get (org A): %v", err)
	}

	if _, err := h.tenancy.UpdateOrganization(ctx, acB, tenancy.UpdateOrganizationInput{Slug: &orgA.Slug}); err == nil {
		t.Fatal("expected renaming to another organization's slug to fail")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeConflict {
		t.Fatalf("expected CONFLICT, got %v", err)
	}
}

// LeaveOrganization is the self-service counterpart to RemoveMember — a
// plain member (no organization.manage) can remove themselves even though
// they couldn't remove anyone else.
func TestTenancy_LeaveOrganizationRemovesSelf(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ownerAC, _ := h.newOwnerContext(t, ctx, "leaveorg-owner@nodera.dev")
	memberAC := h.newMemberContext(t, ctx, ownerAC.OrganizationID, "leaveorg-member@nodera.dev")

	if err := h.tenancy.LeaveOrganization(ctx, memberAC); err != nil {
		t.Fatalf("LeaveOrganization: %v", err)
	}

	members, err := h.rbac.ListMembers(ctx, ownerAC)
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	for _, m := range members {
		if m.Email == "leaveorg-member@nodera.dev" {
			t.Fatalf("expected the departed member to no longer appear, got %+v", m)
		}
	}
}

// The sole owner can't leave any more than they could remove themselves
// via the admin path — same last-owner guard, same reason.
func TestTenancy_LeaveOrganizationRefusesLastOwner(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "leaveorg-lastowner-owner@nodera.dev")

	if err := h.tenancy.LeaveOrganization(ctx, ac); err == nil {
		t.Fatal("expected the last owner leaving to fail")
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
