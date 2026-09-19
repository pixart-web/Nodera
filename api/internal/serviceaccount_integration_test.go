package integration_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/identity"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/testhelpers"
)

func TestServiceAccountsCreateListDisable(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "sa-owner@nodera.dev")

	sa, err := h.identity.CreateServiceAccount(ctx, ac, "ci-bot", "used by the CI pipeline")
	if err != nil {
		t.Fatalf("CreateServiceAccount: %v", err)
	}
	if sa.Status != "active" {
		t.Fatalf("expected a freshly created service account to be active, got %q", sa.Status)
	}

	list, err := h.identity.ListServiceAccounts(ctx, ac)
	if err != nil {
		t.Fatalf("ListServiceAccounts: %v", err)
	}
	if len(list) != 1 || list[0].ID != sa.ID {
		t.Fatalf("expected exactly the created service account in the list, got %+v", list)
	}

	if err := h.identity.DisableServiceAccount(ctx, ac, sa.ID); err != nil {
		t.Fatalf("DisableServiceAccount: %v", err)
	}
}

// Enable reverses Disable's status flip but must not resurrect the tokens
// Disable revoked — that revocation is meant to be permanent, not merely a
// side effect of the account being temporarily disabled.
func TestServiceAccountEnableReversesDisableButNotTokenRevocation(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "sa-enable-owner@nodera.dev")

	sa, err := h.identity.CreateServiceAccount(ctx, ac, "reactivate-bot", "")
	if err != nil {
		t.Fatalf("CreateServiceAccount: %v", err)
	}
	raw, _, err := h.identity.CreateAPITokenForServiceAccount(ctx, ac, sa.ID, "old-key", []string{"applications.read"}, nil)
	if err != nil {
		t.Fatalf("CreateAPITokenForServiceAccount: %v", err)
	}

	if err := h.identity.DisableServiceAccount(ctx, ac, sa.ID); err != nil {
		t.Fatalf("DisableServiceAccount: %v", err)
	}
	if err := h.identity.EnableServiceAccount(ctx, ac, sa.ID); err != nil {
		t.Fatalf("EnableServiceAccount: %v", err)
	}

	list, err := h.identity.ListServiceAccounts(ctx, ac)
	if err != nil {
		t.Fatalf("ListServiceAccounts: %v", err)
	}
	var found bool
	for _, s := range list {
		if s.ID == sa.ID {
			found = true
			if s.Status != "active" {
				t.Fatalf("expected re-enabled service account to be active, got %q", s.Status)
			}
		}
	}
	if !found {
		t.Fatal("expected the service account to still exist after enable")
	}

	// The pre-disable token must stay dead even after re-enabling.
	if _, err := h.identity.AuthContextForAPIToken(ctx, raw, "test-correlation"); err == nil {
		t.Fatal("expected the token revoked at disable-time to remain revoked after re-enabling")
	}

	// A freshly minted token for the now-active account works normally.
	newRaw, _, err := h.identity.CreateAPITokenForServiceAccount(ctx, ac, sa.ID, "new-key", []string{"applications.read"}, nil)
	if err != nil {
		t.Fatalf("expected minting a new token for a re-enabled service account to succeed: %v", err)
	}
	if _, err := h.identity.AuthContextForAPIToken(ctx, newRaw, "test-correlation"); err != nil {
		t.Fatalf("expected the freshly minted token to authenticate: %v", err)
	}
}

func TestServiceAccountUpdateChangesOnlyProvidedFields(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "sa-update-owner@nodera.dev")

	sa, err := h.identity.CreateServiceAccount(ctx, ac, "original-name", "original description")
	if err != nil {
		t.Fatalf("CreateServiceAccount: %v", err)
	}

	newName := "renamed-bot"
	updated, err := h.identity.UpdateServiceAccount(ctx, ac, sa.ID, identity.UpdateServiceAccountInput{Name: &newName})
	if err != nil {
		t.Fatalf("UpdateServiceAccount: %v", err)
	}
	if updated.Name != "renamed-bot" {
		t.Fatalf("expected name to be updated, got %q", updated.Name)
	}
	if updated.Description != "original description" {
		t.Fatalf("untouched field Description changed: got %q", updated.Description)
	}
	if updated.Status != sa.Status {
		t.Fatalf("Update must never change status, got %q", updated.Status)
	}
}

func TestServiceAccountEnableUpdateRequireOrganizationManage(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "sa-enable-perm-owner@nodera.dev")

	sa, err := h.identity.CreateServiceAccount(ctx, ac, "guarded-bot", "")
	if err != nil {
		t.Fatalf("CreateServiceAccount: %v", err)
	}
	if err := h.identity.DisableServiceAccount(ctx, ac, sa.ID); err != nil {
		t.Fatalf("DisableServiceAccount: %v", err)
	}

	memberAC := h.newMemberContext(t, ctx, ac.OrganizationID, "sa-enable-perm-member@nodera.dev")

	if err := h.identity.EnableServiceAccount(ctx, memberAC, sa.ID); err == nil {
		t.Fatal("expected a member without organization.manage to be forbidden from enabling a service account")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}

	newName := "should-not-apply"
	if _, err := h.identity.UpdateServiceAccount(ctx, memberAC, sa.ID, identity.UpdateServiceAccountInput{Name: &newName}); err == nil {
		t.Fatal("expected a member without organization.manage to be forbidden from updating a service account")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}
}

// A token issued for a service account authenticates as that service
// account (not the issuing user), with exactly the granted scopes as its
// permissions — proving service-account-issued tokens are real, not just
// schema.
func TestServiceAccountAPITokenAuthenticatesAsServiceAccount(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "sa-token-owner@nodera.dev")

	sa, err := h.identity.CreateServiceAccount(ctx, ac, "deploy-bot", "")
	if err != nil {
		t.Fatalf("CreateServiceAccount: %v", err)
	}

	raw, info, err := h.identity.CreateAPITokenForServiceAccount(ctx, ac, sa.ID, "ci-key", []string{"applications.read"}, nil)
	if err != nil {
		t.Fatalf("CreateAPITokenForServiceAccount: %v", err)
	}
	if info.Name != "ci-key" {
		t.Fatalf("unexpected token name: %s", info.Name)
	}

	tokenAC, err := h.identity.AuthContextForAPIToken(ctx, raw, "test-correlation")
	if err != nil {
		t.Fatalf("AuthContextForAPIToken: %v", err)
	}
	if tokenAC.ActorID != sa.ID {
		t.Fatalf("expected the token to authenticate as the service account, got actor %s", tokenAC.ActorID)
	}
	if tokenAC.ActorLabel != "deploy-bot" {
		t.Fatalf("expected actor label 'deploy-bot', got %q", tokenAC.ActorLabel)
	}
	if !tokenAC.HasPermission("applications.read") || tokenAC.HasPermission("applications.deploy") {
		t.Fatalf("token permissions should exactly equal its granted scopes, got %+v", tokenAC.Permissions)
	}

	// Disabling the service account must immediately cut off its
	// outstanding token, not just block minting new ones.
	if err := h.identity.DisableServiceAccount(ctx, ac, sa.ID); err != nil {
		t.Fatalf("DisableServiceAccount: %v", err)
	}
	if _, _, err := h.identity.CreateAPITokenForServiceAccount(ctx, ac, sa.ID, "another-key", []string{"applications.read"}, nil); err == nil {
		t.Fatal("expected minting a new token for a disabled service account to fail")
	}
	if _, err := h.identity.AuthContextForAPIToken(ctx, raw, "test-correlation"); err == nil {
		t.Fatal("expected the already-issued token to stop working once its service account is disabled")
	}
}

// A caller cannot mint a service-account token with a scope beyond their
// own permissions — same no-privilege-escalation rule as user-owned tokens.
func TestServiceAccountAPIToken_CannotExceedCallerPermissions(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "sa-scope-owner@nodera.dev")

	sa, err := h.identity.CreateServiceAccount(ctx, ac, "bot", "")
	if err != nil {
		t.Fatalf("CreateServiceAccount: %v", err)
	}

	if _, _, err := h.identity.CreateAPITokenForServiceAccount(ctx, ac, sa.ID, "bad", []string{"not.a.real.permission"}, nil); err == nil {
		t.Fatal("expected an error minting a service account token with an ungranted scope")
	}
}

// The org-admin listing/revocation endpoints see and can revoke a token
// that belongs to someone else entirely, unlike the self-scoped ones.
func TestAdminAPITokenManagement(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ownerAC, _ := h.newOwnerContext(t, ctx, "admin-owner@nodera.dev")

	sa, err := h.identity.CreateServiceAccount(ctx, ownerAC, "someone-elses-bot", "")
	if err != nil {
		t.Fatalf("CreateServiceAccount: %v", err)
	}
	_, saTokenInfo, err := h.identity.CreateAPITokenForServiceAccount(ctx, ownerAC, sa.ID, "sa-key", []string{"infrastructure.read"}, nil)
	if err != nil {
		t.Fatalf("CreateAPITokenForServiceAccount: %v", err)
	}

	// The owner's own self-scoped list must NOT include the service
	// account's token (it isn't theirs).
	ownTokens, err := h.identity.ListAPITokens(ctx, ownerAC)
	if err != nil {
		t.Fatalf("ListAPITokens: %v", err)
	}
	for _, tok := range ownTokens {
		if tok.ID == saTokenInfo.ID {
			t.Fatal("a service account's token must not appear in a user's self-scoped token list")
		}
	}

	// The admin listing does include it.
	adminTokens, err := h.identity.AdminListAPITokens(ctx, ownerAC)
	if err != nil {
		t.Fatalf("AdminListAPITokens: %v", err)
	}
	var found bool
	for _, tok := range adminTokens {
		if tok.ID == saTokenInfo.ID {
			found = true
			if tok.OwnerType != "service_account" || tok.OwnerLabel != "someone-elses-bot" {
				t.Fatalf("unexpected owner info on admin listing: %+v", tok)
			}
		}
	}
	if !found {
		t.Fatal("expected the service account's token to appear in the admin listing")
	}

	if err := h.identity.AdminRevokeAPIToken(ctx, ownerAC, saTokenInfo.ID); err != nil {
		t.Fatalf("AdminRevokeAPIToken: %v", err)
	}

	adminTokensAfter, err := h.identity.AdminListAPITokens(ctx, ownerAC)
	if err != nil {
		t.Fatalf("AdminListAPITokens (after revoke): %v", err)
	}
	for _, tok := range adminTokensAfter {
		if tok.ID == saTokenInfo.ID {
			t.Fatal("expected the revoked token to no longer appear in the admin listing")
		}
	}
}

// A 'member' (no organization.manage) cannot use any of the admin/service
// account endpoints.
func TestServiceAccountManagement_RequiresOrganizationManage(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ownerAC, _ := h.newOwnerContext(t, ctx, "member-perm-owner@nodera.dev")

	memberEmail := "member-perm-member@nodera.dev"
	memberUser, err := h.identity.SignUp(ctx, memberEmail, "correct horse battery staple 9", "Member")
	if err != nil {
		t.Fatalf("SignUp: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organization_members (organization_id, user_id) VALUES ($1, $2)`, ownerAC.OrganizationID, memberUser.ID); err != nil {
		t.Fatalf("add member: %v", err)
	}
	memberRoleID := uuid.MustParse("00000000-0000-0000-0000-000000000003")
	if _, err := pool.Exec(ctx, `INSERT INTO organization_member_roles (organization_id, user_id, role_id) VALUES ($1, $2, $3)`, ownerAC.OrganizationID, memberUser.ID, memberRoleID); err != nil {
		t.Fatalf("grant member role: %v", err)
	}
	memberToken, _, err := h.identity.Login(ctx, memberEmail, "correct horse battery staple 9", "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	memberAC, err := h.identity.AuthContextForSession(ctx, memberToken, ownerAC.OrganizationID, "test-correlation")
	if err != nil {
		t.Fatalf("AuthContextForSession: %v", err)
	}

	if _, err := h.identity.CreateServiceAccount(ctx, memberAC, "should-fail", ""); err == nil {
		t.Fatal("expected a 'member' to be forbidden from creating a service account")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}

	if _, err := h.identity.AdminListAPITokens(ctx, memberAC); err == nil {
		t.Fatal("expected a 'member' to be forbidden from the admin token listing")
	}
}
