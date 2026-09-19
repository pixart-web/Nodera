package integration_test

import (
	"context"
	"testing"

	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platformauth"
	"github.com/nodera/nodera/internal/testhelpers"
)

// Identity events (signup, login, failed login, logout, password change,
// session revocation) happen before an organization is ever selected and
// so are written to the platform scope of the audit log (organization_id
// NULL — internal/identity/identity.go's recordIdentityAudit), not any
// one organization's own audit trail. This test proves the whole chain:
// the events are actually recorded, queryable only via QueryPlatform by a
// platform.audit.read holder, invisible to an ordinary organization
// audit.read query, and never leak the password itself.
func TestPlatformAudit_IdentityEventsAreRecordedAndQueryableOnlyByPlatformAdmin(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)

	email := "platform-audit-subject@nodera.dev"
	u, err := h.identity.SignUp(ctx, email, testPassword, "Audit Subject")
	if err != nil {
		t.Fatalf("SignUp: %v", err)
	}

	token, _, err := h.identity.Login(ctx, email, testPassword, "203.0.113.7", "test-agent/1.0")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	// A failed login (wrong password) against the same real account.
	if _, _, err := h.identity.Login(ctx, email, "wrong password entirely", "203.0.113.7", "test-agent/1.0"); err == nil {
		t.Fatal("expected the wrong-password login to fail")
	}

	if err := h.identity.ChangePassword(ctx, u.ID, token, testPassword, "a brand new password 2"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	// ChangePassword revokes every other session but leaves the current
	// one; log back in with the new password to get a fresh token to log
	// out with, exercising the logout audit path too.
	token2, _, err := h.identity.Login(ctx, email, "a brand new password 2", "203.0.113.7", "test-agent/1.0")
	if err != nil {
		t.Fatalf("Login with new password: %v", err)
	}
	if err := h.identity.Logout(ctx, token2); err != nil {
		t.Fatalf("Logout: %v", err)
	}

	// A separate platform administrator, granted platform.audit.read,
	// queries the platform-scope audit log.
	adminAC, _ := h.newOwnerContext(t, ctx, "platform-audit-admin@nodera.dev")
	h.grantPlatformPermission(t, ctx, adminAC.ActorID, platformauth.PermManagePlatformAdmins)
	h.grantPlatformPermission(t, ctx, adminAC.ActorID, "platform.audit.read")

	records, err := h.audit.QueryPlatform(ctx, adminAC, audit.QueryFilter{ResourceID: u.ID.String(), Limit: 100})
	if err != nil {
		t.Fatalf("QueryPlatform: %v", err)
	}

	wantActions := map[string]bool{
		"identity.user.signed_up":        false,
		"identity.user.logged_in":        false,
		"identity.user.login_failed":     false,
		"identity.user.password_changed": false,
		"identity.user.logged_out":       false,
	}
	for _, r := range records {
		if r.OrganizationID != nil {
			t.Fatalf("expected every platform-audit record to have a nil organization_id, got %+v", r)
		}
		if r.ActorUserID == nil || *r.ActorUserID != u.ID {
			t.Fatalf("expected actor_user_id to identify the subject user, got %+v", r.ActorUserID)
		}
		if _, ok := wantActions[r.Action]; ok {
			wantActions[r.Action] = true
		}
		// Never leak credentials into the audit trail.
		asJSON := r.ActorLabel + r.Action + r.ResourceID
		if containsSecret(asJSON) {
			t.Fatalf("audit record appears to contain a credential-shaped value: %+v", r)
		}
	}
	for action, seen := range wantActions {
		if !seen {
			t.Errorf("expected a %q platform-audit record, none found in %+v", action, records)
		}
	}

	// An ordinary organization's own audit.read query must never surface
	// these platform-scope rows — they are a different scope entirely,
	// not merely filtered out by organization_id coincidentally.
	orgRecords, err := h.audit.Query(ctx, adminAC, audit.QueryFilter{OrganizationID: adminAC.OrganizationID, Limit: 1000})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	for _, r := range orgRecords {
		if r.Action == "identity.user.signed_up" || r.Action == "identity.user.logged_in" {
			t.Fatalf("expected identity events to never appear in an organization-scoped audit query, found %+v", r)
		}
	}
}

// A user who holds no platform.audit.read grant — even an organization
// owner with full organization-level audit.read — is forbidden from
// QueryPlatform. Platform and organization audit are genuinely separate
// authorization scopes, not the same permission reused.
func TestPlatformAudit_RequiresPlatformAuditReadPermission(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "no-platform-audit-grant@nodera.dev")

	if _, err := h.audit.QueryPlatform(ctx, ac, audit.QueryFilter{}); err == nil {
		t.Fatal("expected QueryPlatform to require platform.audit.read")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}
}

func containsSecret(s string) bool {
	// Cheap heuristic sufficient for this test's purpose: the exact
	// passwords used above would appear verbatim if somehow leaked into
	// a label/action/resource_id string.
	needles := []string{testPassword, "a brand new password 2", "wrong password entirely"}
	for _, n := range needles {
		if len(s) >= len(n) {
			for i := 0; i+len(n) <= len(s); i++ {
				if s[i:i+len(n)] == n {
					return true
				}
			}
		}
	}
	return false
}
