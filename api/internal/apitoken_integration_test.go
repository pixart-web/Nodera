package integration_test

import (
	"context"
	"strings"
	"testing"

	"github.com/nodera/nodera/internal/audit"
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

// CreateAPIToken/RevokeAPIToken now write real audit entries — verifies
// the entries are actually queryable, not just written, and that the
// raw token value never appears in the recorded state (only the prefix).
func TestAPIToken_WritesAuditEntries(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "token-audit-owner@nodera.dev")

	raw, info, err := h.identity.CreateAPIToken(ctx, ac, "audited-token", []string{"infrastructure.read"}, nil)
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}
	if err := h.identity.RevokeAPIToken(ctx, ac, info.ID); err != nil {
		t.Fatalf("RevokeAPIToken: %v", err)
	}

	records, err := h.audit.Query(ctx, ac, audit.QueryFilter{
		OrganizationID: ac.OrganizationID, ResourceType: "api_token", ResourceID: info.ID.String(),
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 audit entries (created, revoked), got %d: %+v", len(records), records)
	}
	var sawCreated, sawRevoked bool
	for _, r := range records {
		switch r.Action {
		case "identity.api_token.created":
			sawCreated = true
		case "identity.api_token.revoked":
			sawRevoked = true
		}
	}
	if !sawCreated || !sawRevoked {
		t.Fatalf("expected both created and revoked actions, got %+v", records)
	}

	// The raw token value must never leak into the audit trail.
	rows, err := pool.Query(ctx, `SELECT COALESCE(resulting_state::text, '') FROM audit_log WHERE resource_id = $1`, info.ID.String())
	if err != nil {
		t.Fatalf("query audit_log directly: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		if err := rows.Scan(&state); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if state != "" && strings.Contains(state, raw) {
			t.Fatal("the raw API token value leaked into an audit_log row")
		}
	}
}

func TestAPIToken_UpdateRenamesWithoutTouchingScopesOrValue(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "token-rename-owner@nodera.dev")

	raw, info, err := h.identity.CreateAPIToken(ctx, ac, "original-name", []string{"infrastructure.read"}, nil)
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}

	updated, err := h.identity.UpdateAPIToken(ctx, ac, info.ID, "renamed-token")
	if err != nil {
		t.Fatalf("UpdateAPIToken: %v", err)
	}
	if updated.Name != "renamed-token" {
		t.Fatalf("expected the name to be updated, got %q", updated.Name)
	}
	if updated.TokenPrefix != info.TokenPrefix {
		t.Fatalf("rename must not change the token prefix, got %q want %q", updated.TokenPrefix, info.TokenPrefix)
	}
	if len(updated.Scopes) != 1 || updated.Scopes[0] != "infrastructure.read" {
		t.Fatalf("rename must not change scopes, got %+v", updated.Scopes)
	}

	// The token itself still authenticates exactly as before — a rename
	// never invalidates or rotates the underlying credential.
	tokenAC, err := h.identity.AuthContextForAPIToken(ctx, raw, "test-correlation")
	if err != nil {
		t.Fatalf("AuthContextForAPIToken after rename: %v", err)
	}
	if !tokenAC.HasPermission("infrastructure.read") {
		t.Fatal("expected the token to still authenticate with its original scope after rename")
	}

	if _, err := h.identity.UpdateAPIToken(ctx, ac, info.ID, ""); err == nil {
		t.Fatal("expected an empty name to be rejected")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeValidation {
		t.Fatalf("expected VALIDATION_ERROR, got %v", err)
	}
}

// A caller can't rename a token they don't own — even another user's
// token within the same organization.
func TestAPIToken_UpdateScopedToOwnTokensOnly(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "token-rename-other-owner@nodera.dev")
	otherAC := h.newMemberContext(t, ctx, ac.OrganizationID, "token-rename-other-member@nodera.dev")

	_, info, err := h.identity.CreateAPIToken(ctx, ac, "owners-token", []string{"infrastructure.read"}, nil)
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}

	if _, err := h.identity.UpdateAPIToken(ctx, otherAC, info.ID, "hijacked-name"); err == nil {
		t.Fatal("expected a caller who doesn't own the token to be refused")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeNotFound {
		t.Fatalf("expected NOT_FOUND, got %v", err)
	}
}
