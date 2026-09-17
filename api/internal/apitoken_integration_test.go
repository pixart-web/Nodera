package integration_test

import (
	"context"
	"testing"

	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/testhelpers"
)

func TestAPITokenCreateAuthenticateAndRevoke(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "token-owner@nodera.dev")

	// Cannot mint a token with a scope the caller doesn't hold.
	if _, _, err := h.identity.CreateAPIToken(ctx, ac, "bad-token", []string{"not.a.real.permission"}, nil); err == nil {
		t.Fatal("expected an error minting a token with an ungranted scope")
	}

	raw, info, err := h.identity.CreateAPIToken(ctx, ac, "ci-token", []string{"infrastructure.read", "audit.read"}, nil)
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}
	if info.TokenPrefix != raw[:8] {
		t.Fatalf("token prefix mismatch: got %q want %q", info.TokenPrefix, raw[:8])
	}

	tokenAC, err := h.identity.AuthContextForAPIToken(ctx, raw, "test-correlation")
	if err != nil {
		t.Fatalf("AuthContextForAPIToken: %v", err)
	}
	if tokenAC.OrganizationID != ac.OrganizationID {
		t.Fatal("token AuthContext resolved to the wrong organization")
	}
	if !tokenAC.HasPermission("infrastructure.read") || tokenAC.HasPermission("infrastructure.manage") {
		t.Fatal("token AuthContext permissions should exactly equal its granted scopes")
	}

	if err := h.identity.RevokeAPIToken(ctx, ac, info.ID); err != nil {
		t.Fatalf("RevokeAPIToken: %v", err)
	}

	_, err = h.identity.AuthContextForAPIToken(ctx, raw, "test-correlation")
	if err == nil {
		t.Fatal("expected a revoked token to fail authentication")
	}
	if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeUnauthenticated {
		t.Fatalf("expected UNAUTHENTICATED for a revoked token, got %v", err)
	}
}
