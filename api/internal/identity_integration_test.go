package integration_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

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

// RevokeAllOtherSessions is a direct action with the identical
// "current session survives, every other one dies" behavior
// ChangePassword only gives as a side effect.
func TestIdentity_RevokeAllOtherSessionsKeepsCurrentAlive(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)

	u, err := h.identity.SignUp(ctx, "revoke-others-sessions@nodera.dev", testPassword, "Test User")
	if err != nil {
		t.Fatalf("SignUp: %v", err)
	}

	tokenA, _, err := h.identity.Login(ctx, "revoke-others-sessions@nodera.dev", testPassword, "127.0.0.1", "session-a")
	if err != nil {
		t.Fatalf("Login (session A): %v", err)
	}
	tokenB, _, err := h.identity.Login(ctx, "revoke-others-sessions@nodera.dev", testPassword, "127.0.0.1", "session-b")
	if err != nil {
		t.Fatalf("Login (session B): %v", err)
	}
	tokenC, _, err := h.identity.Login(ctx, "revoke-others-sessions@nodera.dev", testPassword, "127.0.0.1", "session-c")
	if err != nil {
		t.Fatalf("Login (session C): %v", err)
	}

	if err := h.identity.RevokeAllOtherSessions(ctx, u.ID, tokenA); err != nil {
		t.Fatalf("RevokeAllOtherSessions: %v", err)
	}

	if _, err := h.identity.UserIDForSession(ctx, tokenA); err != nil {
		t.Fatalf("expected the calling session A to remain valid, got %v", err)
	}
	if _, err := h.identity.UserIDForSession(ctx, tokenB); err == nil {
		t.Fatal("expected session B to be revoked")
	} else if err != identity.ErrSessionInvalid {
		t.Fatalf("expected ErrSessionInvalid for session B, got %v", err)
	}
	if _, err := h.identity.UserIDForSession(ctx, tokenC); err == nil {
		t.Fatal("expected session C to be revoked")
	} else if err != identity.ErrSessionInvalid {
		t.Fatalf("expected ErrSessionInvalid for session C, got %v", err)
	}

	// An absent/invalid current token means "nothing to exclude," not an
	// error — same tradeoff ChangePassword makes.
	if err := h.identity.RevokeAllOtherSessions(ctx, u.ID, ""); err != nil {
		t.Fatalf("RevokeAllOtherSessions with no current token: %v", err)
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

// ListSessions returns every active session for the account, marking
// exactly the one the caller is currently using — not by coincidence of
// ordering, but by resolving the actual token passed in.
func TestIdentity_ListSessionsMarksCurrentSession(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)

	u, err := h.identity.SignUp(ctx, "sessions-list@nodera.dev", testPassword, "Test User")
	if err != nil {
		t.Fatalf("SignUp: %v", err)
	}
	if _, _, err := h.identity.Login(ctx, "sessions-list@nodera.dev", testPassword, "127.0.0.1", "device-a"); err != nil {
		t.Fatalf("Login (A): %v", err)
	}
	tokenB, _, err := h.identity.Login(ctx, "sessions-list@nodera.dev", testPassword, "10.0.0.5", "device-b")
	if err != nil {
		t.Fatalf("Login (B): %v", err)
	}

	sessions, err := h.identity.ListSessions(ctx, u.ID, tokenB)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 active sessions, got %d: %+v", len(sessions), sessions)
	}

	var currentCount int
	for _, s := range sessions {
		if s.IsCurrent {
			currentCount++
			if s.UserAgent != "device-b" {
				t.Fatalf("expected the session marked current to be the one tokenB resolves to, got %+v", s)
			}
		}
	}
	if currentCount != 1 {
		t.Fatalf("expected exactly 1 session marked current, got %d", currentCount)
	}
}

// RevokeSession lets a user log out one specific session (e.g. "log out
// that other device") without affecting any of their other sessions, and
// refuses to touch a session belonging to someone else.
func TestIdentity_RevokeSessionOnlyAffectsTargetAndOwner(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)

	u1, err := h.identity.SignUp(ctx, "sessions-revoke-1@nodera.dev", testPassword, "User One")
	if err != nil {
		t.Fatalf("SignUp (1): %v", err)
	}
	u2, err := h.identity.SignUp(ctx, "sessions-revoke-2@nodera.dev", testPassword, "User Two")
	if err != nil {
		t.Fatalf("SignUp (2): %v", err)
	}

	tokenA, _, err := h.identity.Login(ctx, "sessions-revoke-1@nodera.dev", testPassword, "127.0.0.1", "device-a")
	if err != nil {
		t.Fatalf("Login (A): %v", err)
	}
	tokenB, _, err := h.identity.Login(ctx, "sessions-revoke-1@nodera.dev", testPassword, "127.0.0.1", "device-b")
	if err != nil {
		t.Fatalf("Login (B): %v", err)
	}

	sessions, err := h.identity.ListSessions(ctx, u1.ID, "")
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	var deviceBID uuid.UUID
	for _, s := range sessions {
		if s.UserAgent == "device-b" {
			deviceBID = s.ID
		}
	}
	if deviceBID == uuid.Nil {
		t.Fatal("expected to find device-b's session in the list")
	}

	// User 2 cannot revoke user 1's session by ID.
	if err := h.identity.RevokeSession(ctx, u2.ID, deviceBID); err == nil {
		t.Fatal("expected revoking another user's session to fail")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeNotFound {
		t.Fatalf("expected NOT_FOUND, got %v", err)
	}

	if err := h.identity.RevokeSession(ctx, u1.ID, deviceBID); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}

	if _, err := h.identity.UserIDForSession(ctx, tokenB); err == nil {
		t.Fatal("expected device-b's session to be revoked")
	} else if err != identity.ErrSessionInvalid {
		t.Fatalf("expected ErrSessionInvalid, got %v", err)
	}
	if _, err := h.identity.UserIDForSession(ctx, tokenA); err != nil {
		t.Fatalf("expected device-a's session to remain valid, got %v", err)
	}
}
