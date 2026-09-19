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
