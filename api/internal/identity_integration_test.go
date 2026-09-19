package integration_test

import (
	"context"
	"testing"

	"github.com/nodera/nodera/internal/identity"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/testhelpers"
)

const testPassword = "correct horse battery staple 9"

func TestIdentity_UpdateProfileChangesDisplayName(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)

	u, err := h.identity.SignUp(ctx, "profile-update@nodera.dev", testPassword, "Original Name")
	if err != nil {
		t.Fatalf("SignUp: %v", err)
	}

	updated, err := h.identity.UpdateProfile(ctx, u.ID, "New Name")
	if err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	if updated.DisplayName != "New Name" {
		t.Fatalf("expected display_name to be updated, got %q", updated.DisplayName)
	}
	if updated.Email != u.Email {
		t.Fatalf("expected email to be unchanged, got %q", updated.Email)
	}
}

func TestIdentity_UpdateProfileRejectsEmptyName(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)

	u, err := h.identity.SignUp(ctx, "profile-empty@nodera.dev", testPassword, "Original Name")
	if err != nil {
		t.Fatalf("SignUp: %v", err)
	}

	if _, err := h.identity.UpdateProfile(ctx, u.ID, ""); err == nil {
		t.Fatal("expected an empty display name to be rejected")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeValidation {
		t.Fatalf("expected VALIDATION_ERROR, got %v", err)
	}
}

func TestIdentity_ChangePasswordRequiresCorrectCurrentPassword(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)

	u, err := h.identity.SignUp(ctx, "changepw-wrong@nodera.dev", testPassword, "Test User")
	if err != nil {
		t.Fatalf("SignUp: %v", err)
	}

	err = h.identity.ChangePassword(ctx, u.ID, "", "wrong current password", "a new strong password 123")
	if err == nil {
		t.Fatal("expected an incorrect current password to be rejected")
	}
	if _, ok := err.(*apierr.Error); !ok {
		t.Fatalf("expected an *apierr.Error, got %v", err)
	}
}

// Changing the password revokes every other active session but leaves the
// session that made the request untouched — you shouldn't get logged out
// by your own password change, but every other session (another device,
// or one an attacker holds) should stop working immediately.
func TestIdentity_ChangePasswordRevokesOtherSessionsButNotCurrent(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)

	u, err := h.identity.SignUp(ctx, "changepw-sessions@nodera.dev", testPassword, "Test User")
	if err != nil {
		t.Fatalf("SignUp: %v", err)
	}

	tokenA, _, err := h.identity.Login(ctx, "changepw-sessions@nodera.dev", testPassword, "127.0.0.1", "session-a")
	if err != nil {
		t.Fatalf("Login (session A): %v", err)
	}
	tokenB, _, err := h.identity.Login(ctx, "changepw-sessions@nodera.dev", testPassword, "127.0.0.1", "session-b")
	if err != nil {
		t.Fatalf("Login (session B): %v", err)
	}

	if err := h.identity.ChangePassword(ctx, u.ID, tokenA, testPassword, "a new strong password 123"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	if _, err := h.identity.UserIDForSession(ctx, tokenA); err != nil {
		t.Fatalf("expected session A (the one that made the request) to remain valid, got %v", err)
	}
	if _, err := h.identity.UserIDForSession(ctx, tokenB); err == nil {
		t.Fatal("expected session B to be revoked by the password change")
	} else if err != identity.ErrSessionInvalid {
		t.Fatalf("expected ErrSessionInvalid, got %v", err)
	}
}

// After a successful change, the old password no longer works and the new
// one does — proving the rotation actually took effect, not just that the
// call returned success.
func TestIdentity_ChangePasswordTakesEffectForFutureLogins(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)

	u, err := h.identity.SignUp(ctx, "changepw-relogin@nodera.dev", testPassword, "Test User")
	if err != nil {
		t.Fatalf("SignUp: %v", err)
	}

	if err := h.identity.ChangePassword(ctx, u.ID, "", testPassword, "a brand new password 456"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	if _, _, err := h.identity.Login(ctx, "changepw-relogin@nodera.dev", testPassword, "127.0.0.1", "test"); err == nil {
		t.Fatal("expected login with the old password to fail after it was changed")
	}
	if _, _, err := h.identity.Login(ctx, "changepw-relogin@nodera.dev", "a brand new password 456", "127.0.0.1", "test"); err != nil {
		t.Fatalf("expected login with the new password to succeed, got %v", err)
	}
}
