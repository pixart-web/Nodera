package integration_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/ai"
	"github.com/nodera/nodera/internal/ai/providers"
	"github.com/nodera/nodera/internal/ai/providers/localecho"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/testhelpers"
)

func TestAIProfileCreateAndChatRoundTrip(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "ai-owner@nodera.dev")

	aiSvc := ai.New(pool, h.audit, localecho.New())

	profile, err := aiSvc.CreateProfile(ctx, ac, ai.CreateProfileInput{
		Key:                "test.echo",
		PrivacyLevel:       "internal",
		PreferredModelRefs: []string{"local-echo/echo-1"},
	})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if profile.Key != "test.echo" {
		t.Fatalf("unexpected profile key: %s", profile.Key)
	}

	result, err := aiSvc.Chat(ctx, ac, "test.echo", []providers.Message{
		{Role: "user", Content: "hello nodera"},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if result.Content != "echo: hello nodera" {
		t.Fatalf("unexpected chat response: %q", result.Content)
	}
	if result.ProviderKey != "local-echo" {
		t.Fatalf("expected the local-echo provider to be selected, got %q", result.ProviderKey)
	}

	// A RESTRICTED profile must never resolve to a cloud provider — here it
	// must fail closed (no cloud provider is even registered) rather than
	// silently falling back to local-echo, proving the privacy check runs
	// before model selection, not as an afterthought.
	restricted, err := aiSvc.CreateProfile(ctx, ac, ai.CreateProfileInput{
		Key:                "test.restricted-nonexistent",
		PrivacyLevel:       "restricted",
		PreferredModelRefs: []string{"openai/gpt-fake"}, // no such provider registered
	})
	if err != nil {
		t.Fatalf("CreateProfile (restricted): %v", err)
	}
	if _, err := aiSvc.Chat(ctx, ac, restricted.Key, []providers.Message{{Role: "user", Content: "x"}}); err == nil {
		t.Fatal("expected Chat to fail when no policy-compliant provider is available")
	}
}

func TestAIChatUnknownProfileIsNotFound(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "ai-owner-2@nodera.dev")

	aiSvc := ai.New(pool, h.audit, localecho.New())

	if _, err := aiSvc.Chat(ctx, ac, "does.not.exist", []providers.Message{{Role: "user", Content: "x"}}); err == nil {
		t.Fatal("expected an error for a nonexistent AI profile")
	}
}

func TestAIUpdateProfileChangesOnlyProvidedFields(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "ai-update-owner@nodera.dev")

	aiSvc := ai.New(pool, h.audit, localecho.New())

	profile, err := aiSvc.CreateProfile(ctx, ac, ai.CreateProfileInput{
		Key:                "test.update-only",
		Description:        "original",
		PrivacyLevel:       "internal",
		PreferredModelRefs: []string{"local-echo/echo-1"},
		MaxTokens:          2048,
	})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	newDescription := "updated"
	updated, err := aiSvc.UpdateProfile(ctx, ac, profile.ID, ai.UpdateProfileInput{
		Description: &newDescription,
	})
	if err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	if updated.Description != "updated" {
		t.Fatalf("expected description to be updated, got %q", updated.Description)
	}
	if updated.Key != profile.Key {
		t.Fatalf("Key must never change via Update: got %q", updated.Key)
	}
	if updated.MaxTokens != profile.MaxTokens {
		t.Fatalf("untouched field MaxTokens changed: got %d, want %d", updated.MaxTokens, profile.MaxTokens)
	}
	if len(updated.PreferredModelRefs) != 1 || updated.PreferredModelRefs[0] != "local-echo/echo-1" {
		t.Fatalf("untouched field PreferredModelRefs changed: %v", updated.PreferredModelRefs)
	}
}

func TestAIUpdateDeleteProfileRequireManagePermission(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "ai-perm-owner@nodera.dev")

	aiSvc := ai.New(pool, h.audit, localecho.New())

	profile, err := aiSvc.CreateProfile(ctx, ac, ai.CreateProfileInput{
		Key:                "test.perm-guard",
		PrivacyLevel:       "internal",
		PreferredModelRefs: []string{"local-echo/echo-1"},
	})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	memberAC := h.newMemberContext(t, ctx, ac.OrganizationID, "ai-perm-member@nodera.dev")

	desc := "member should not be able to set this"
	if _, err := aiSvc.UpdateProfile(ctx, memberAC, profile.ID, ai.UpdateProfileInput{Description: &desc}); err == nil {
		t.Fatal("expected a member without ai.manage to be forbidden from updating a profile")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}

	if err := aiSvc.DeleteProfile(ctx, memberAC, profile.ID); err == nil {
		t.Fatal("expected a member without ai.manage to be forbidden from deleting a profile")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}
}

func TestAIDeleteProfileThenChatFailsNotFound(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "ai-delete-owner@nodera.dev")

	aiSvc := ai.New(pool, h.audit, localecho.New())

	profile, err := aiSvc.CreateProfile(ctx, ac, ai.CreateProfileInput{
		Key:                "test.to-delete",
		PrivacyLevel:       "internal",
		PreferredModelRefs: []string{"local-echo/echo-1"},
	})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	if err := aiSvc.DeleteProfile(ctx, ac, profile.ID); err != nil {
		t.Fatalf("DeleteProfile: %v", err)
	}

	if _, err := aiSvc.Chat(ctx, ac, profile.Key, []providers.Message{{Role: "user", Content: "x"}}); err == nil {
		t.Fatal("expected Chat against a deleted profile's key to fail")
	}

	if err := aiSvc.DeleteProfile(ctx, ac, profile.ID); err == nil {
		t.Fatal("expected deleting an already-deleted profile to fail")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeNotFound {
		t.Fatalf("expected NOT_FOUND, got %v", err)
	}
}

func TestAICannotUpdateOrDeleteSystemDefinedProfile(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "ai-system-owner@nodera.dev")

	aiSvc := ai.New(pool, h.audit, localecho.New())

	// System-defined profiles (organization_id IS NULL) aren't created
	// through the API today, but the schema supports them; insert one
	// directly to prove an org-scoped ai.manage caller can't reach it via
	// Update/Delete even though ListProfiles/Chat can see and use it.
	var systemProfileID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO ai_profiles (organization_id, key, privacy_level, preferred_model_ids)
		VALUES (NULL, 'test.system-defined', 'internal', ARRAY['local-echo/echo-1'])
		RETURNING id
	`).Scan(&systemProfileID); err != nil {
		t.Fatalf("failed to seed system-defined profile: %v", err)
	}

	id, err := uuid.Parse(systemProfileID)
	if err != nil {
		t.Fatalf("parse id: %v", err)
	}

	desc := "should not apply"
	if _, err := aiSvc.UpdateProfile(ctx, ac, id, ai.UpdateProfileInput{Description: &desc}); err == nil {
		t.Fatal("expected updating a system-defined profile via an org-scoped call to fail")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeNotFound {
		t.Fatalf("expected NOT_FOUND, got %v", err)
	}

	if err := aiSvc.DeleteProfile(ctx, ac, id); err == nil {
		t.Fatal("expected deleting a system-defined profile via an org-scoped call to fail")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeNotFound {
		t.Fatalf("expected NOT_FOUND, got %v", err)
	}
}
