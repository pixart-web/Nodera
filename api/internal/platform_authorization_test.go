package integration_test

import (
	"context"
	"testing"

	"github.com/nodera/nodera/internal/ai"
	"github.com/nodera/nodera/internal/ai/providers/localecho"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platformauth"
	"github.com/nodera/nodera/internal/testhelpers"
)

// The AI provider/model registry (internal/ai/registry.go) is
// platform-wide, not organization-scoped. Gating its mutations with an
// organization permission like ai.manage would let any organization admin
// mutate global state merely by administering their own organization —
// this test proves that mismatch is closed: an organization owner (who
// holds every organization permission, including ai.manage) is still
// forbidden from mutating the platform registry without an explicit
// platform.ai.providers.manage/platform.ai.models.manage grant.
func TestPlatformAuth_OrganizationAdminCannotManagePlatformRegistryWithoutGrant(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "no-platform-grant-owner@nodera.dev")

	aiSvc := ai.New(pool, h.audit, h.platform, localecho.New())

	_, err := aiSvc.UpsertProvider(ctx, ac, ai.UpsertProviderInput{
		Key: "unauthorized-provider", Kind: "cloud", DisplayName: "Should Fail", Status: "active",
	})
	if err == nil {
		t.Fatal("expected an organization owner without a platform grant to be forbidden from UpsertProvider")
	}
	if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}

	_, err = aiSvc.UpsertModel(ctx, ac, ai.UpsertModelInput{
		ProviderKey: "local-echo", ModelIdentifier: "unauthorized-model",
	})
	if err == nil {
		t.Fatal("expected an organization owner without a platform grant to be forbidden from UpsertModel")
	}
	if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}

	if err := aiSvc.DeleteProvider(ctx, ac, "local-echo"); err == nil {
		t.Fatal("expected an organization owner without a platform grant to be forbidden from DeleteProvider")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}
}

// The counterpart: a user explicitly granted the platform permission CAN
// mutate the platform registry, proving the gate isn't just closed —
// it's a real, usable authorization path.
func TestPlatformAuth_GrantedPlatformAdminCanManageRegistry(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "platform-admin-owner@nodera.dev")
	h.grantPlatformPermission(t, ctx, ac.ActorID, "platform.ai.providers.manage")
	h.grantPlatformPermission(t, ctx, ac.ActorID, "platform.ai.models.manage")

	aiSvc := ai.New(pool, h.audit, h.platform, localecho.New())

	p, err := aiSvc.UpsertProvider(ctx, ac, ai.UpsertProviderInput{
		Key: "granted-provider", Kind: "cloud", DisplayName: "Should Succeed", Status: "active",
	})
	if err != nil {
		t.Fatalf("UpsertProvider: %v", err)
	}
	if p.Key != "granted-provider" {
		t.Fatalf("unexpected provider: %+v", p)
	}

	if _, err := aiSvc.UpsertModel(ctx, ac, ai.UpsertModelInput{
		ProviderKey: "granted-provider", ModelIdentifier: "granted-model",
	}); err != nil {
		t.Fatalf("UpsertModel: %v", err)
	}

	if err := aiSvc.DeleteProvider(ctx, ac, "granted-provider"); err != nil {
		t.Fatalf("DeleteProvider: %v", err)
	}
}

// A platform permission grant must never widen the holder's organization
// permissions, nor allow cross-tenant access — platform and organization
// authorization are separate axes. A platform-registry admin who is not a
// member of some other organization must still be refused ordinary
// organization-scoped actions there.
func TestPlatformAuth_PlatformGrantDoesNotBypassTenantIsolation(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	platformAdminAC, _ := h.newOwnerContext(t, ctx, "platform-admin-tenant-test@nodera.dev")
	h.grantPlatformPermission(t, ctx, platformAdminAC.ActorID, "platform.ai.providers.manage")

	otherOrgAC, _ := h.newOwnerContext(t, ctx, "other-org-owner-tenant-test@nodera.dev")

	// Sanity: the platform admin genuinely holds no permissions in the
	// other organization — they were never added as a member.
	if platformAdminAC.OrganizationID == otherOrgAC.OrganizationID {
		t.Fatal("test setup bug: expected two distinct organizations")
	}

	// AuthContextForSession for an organization the user does not belong
	// to must fail — a platform grant must not make this succeed.
	token, _, err := h.identity.Login(ctx, "platform-admin-tenant-test@nodera.dev", "correct horse battery staple 9", "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if _, err := h.identity.AuthContextForSession(ctx, token, otherOrgAC.OrganizationID, "test-correlation"); err == nil {
		t.Fatal("expected a platform-registry admin with no membership in another organization to be refused an AuthContext for it")
	}
}

// Granting/revoking platform permissions itself requires
// platform.admins.manage — an ordinary user (even an organization owner)
// cannot self-escalate by calling Grant directly.
func TestPlatformAuth_GrantRevokeRequirePlatformAdminsManage(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "grant-perm-test-owner@nodera.dev")
	targetAC, _ := h.newOwnerContext(t, ctx, "grant-perm-test-target@nodera.dev")

	if err := h.platform.Grant(ctx, ac, targetAC.ActorID, "platform.ai.providers.manage"); err == nil {
		t.Fatal("expected Grant to require platform.admins.manage")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}

	h.grantPlatformPermission(t, ctx, ac.ActorID, platformauth.PermManagePlatformAdmins)

	if err := h.platform.Grant(ctx, ac, targetAC.ActorID, "platform.ai.providers.manage"); err != nil {
		t.Fatalf("Grant (now authorized): %v", err)
	}
	mine, err := h.platform.ListMine(ctx, targetAC)
	if err != nil {
		t.Fatalf("ListMine: %v", err)
	}
	if len(mine) != 1 || mine[0] != "platform.ai.providers.manage" {
		t.Fatalf("expected the target to hold exactly platform.ai.providers.manage, got %+v", mine)
	}

	if err := h.platform.Revoke(ctx, ac, targetAC.ActorID, "platform.ai.providers.manage"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	mine, err = h.platform.ListMine(ctx, targetAC)
	if err != nil {
		t.Fatalf("ListMine after revoke: %v", err)
	}
	if len(mine) != 0 {
		t.Fatalf("expected no remaining platform permissions after revoke, got %+v", mine)
	}
}

// Revoking the last platform.admins.manage grant is refused — otherwise a
// single mistaken API call could permanently lock every human out of
// platform administration with no recovery path short of a manual SQL
// fix, mirroring tenancy's "cannot remove the last owner" guard.
func TestPlatformAuth_CannotRevokeLastPlatformAdmin(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "last-platform-admin@nodera.dev")
	h.grantPlatformPermission(t, ctx, ac.ActorID, platformauth.PermManagePlatformAdmins)

	if err := h.platform.Revoke(ctx, ac, ac.ActorID, platformauth.PermManagePlatformAdmins); err == nil {
		t.Fatal("expected revoking the last platform.admins.manage grant to be refused")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeConflict {
		t.Fatalf("expected CONFLICT, got %v", err)
	}
}

// BootstrapAdmin is idempotent and grants every catalog permission to the
// matching user — the documented, explicit mechanism for establishing the
// first platform administrator (docs/SECURITY.md).
func TestPlatformAuth_BootstrapAdminGrantsFullCatalogIdempotently(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "bootstrap-admin@nodera.dev")

	catalog, err := h.platform.ListCatalog(ctx)
	if err != nil {
		t.Fatalf("ListCatalog: %v", err)
	}
	if len(catalog) == 0 {
		t.Fatal("expected a non-empty platform permission catalog")
	}

	if err := h.platform.BootstrapAdmin(ctx, "bootstrap-admin@nodera.dev"); err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}
	// Running it again must not error or duplicate grants.
	if err := h.platform.BootstrapAdmin(ctx, "bootstrap-admin@nodera.dev"); err != nil {
		t.Fatalf("BootstrapAdmin (second run): %v", err)
	}

	mine, err := h.platform.ListMine(ctx, ac)
	if err != nil {
		t.Fatalf("ListMine: %v", err)
	}
	if len(mine) != len(catalog) {
		t.Fatalf("expected %d permissions granted, got %d: %+v", len(catalog), len(mine), mine)
	}

	// A nonexistent email must not error startup — logged and ignored.
	if err := h.platform.BootstrapAdmin(ctx, "no-such-user@nodera.dev"); err != nil {
		t.Fatalf("BootstrapAdmin for a nonexistent user should not error: %v", err)
	}
}
