package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/ai"
	"github.com/nodera/nodera/internal/ai/providers"
	"github.com/nodera/nodera/internal/ai/providers/localecho"
	"github.com/nodera/nodera/internal/ai/providers/ollama"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/testhelpers"
)

func TestAIProfileCreateAndChatRoundTrip(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "ai-owner@nodera.dev")
	h.grantPlatformPermission(t, ctx, ac.ActorID, "platform.ai.providers.manage")
	h.grantPlatformPermission(t, ctx, ac.ActorID, "platform.ai.models.manage")

	aiSvc := ai.New(pool, h.audit, h.platform, localecho.New())

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

// Every Chat call writes an ai_usage_records row (recordUsage), but until
// now there was no way to ever read them back. ListUsage surfaces the
// exact records a real Chat call produces — both the success case and the
// error path (resolve failure still records a row, with empty
// provider/model and status "error").
func TestAIListUsageReflectsRealChatCalls(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "ai-usage-owner@nodera.dev")
	h.grantPlatformPermission(t, ctx, ac.ActorID, "platform.ai.providers.manage")
	h.grantPlatformPermission(t, ctx, ac.ActorID, "platform.ai.models.manage")

	aiSvc := ai.New(pool, h.audit, h.platform, localecho.New())

	profile, err := aiSvc.CreateProfile(ctx, ac, ai.CreateProfileInput{
		Key:                "test.usage",
		PrivacyLevel:       "internal",
		PreferredModelRefs: []string{"local-echo/echo-1"},
	})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	if _, err := aiSvc.Chat(ctx, ac, "test.usage", []providers.Message{{Role: "user", Content: "hello"}}); err != nil {
		t.Fatalf("Chat: %v", err)
	}

	// A resolve-failure Chat call (restricted profile, no compliant
	// provider) also records a usage row — of the error, not silently.
	restricted, err := aiSvc.CreateProfile(ctx, ac, ai.CreateProfileInput{
		Key:                "test.usage-restricted",
		PrivacyLevel:       "restricted",
		PreferredModelRefs: []string{"openai/gpt-fake"},
	})
	if err != nil {
		t.Fatalf("CreateProfile (restricted): %v", err)
	}
	if _, err := aiSvc.Chat(ctx, ac, restricted.Key, []providers.Message{{Role: "user", Content: "x"}}); err == nil {
		t.Fatal("expected the restricted Chat call to fail")
	}

	records, err := aiSvc.ListUsage(ctx, ac, ai.UsageFilter{Limit: 50})
	if err != nil {
		t.Fatalf("ListUsage: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 usage records, got %d: %+v", len(records), records)
	}

	// Most recent first: the restricted (error) call was second.
	if records[0].ProfileKey != restricted.Key || records[0].Status != "error" {
		t.Fatalf("expected the most recent record to be the failed restricted call, got %+v", records[0])
	}
	if records[0].ProviderKey != "" || records[0].ModelIdentifier != "" {
		t.Fatalf("expected empty provider/model on a resolve-failure record, got %+v", records[0])
	}

	if records[1].ProfileKey != profile.Key || records[1].Status != "success" {
		t.Fatalf("expected the earlier record to be the successful call, got %+v", records[1])
	}
	if records[1].ProviderKey != "local-echo" || records[1].ModelIdentifier != "echo-1" {
		t.Fatalf("expected the successful record to name the real provider/model, got %+v", records[1])
	}
	if records[1].Classification != "local" {
		t.Fatalf("expected local-echo to classify as 'local', got %q", records[1].Classification)
	}
}

func TestAIListUsageIsTenantIsolated(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	acA, _ := h.newOwnerContext(t, ctx, "ai-usage-a@nodera.dev")
	acB, _ := h.newOwnerContext(t, ctx, "ai-usage-b@nodera.dev")

	aiSvc := ai.New(pool, h.audit, h.platform, localecho.New())

	profile, err := aiSvc.CreateProfile(ctx, acA, ai.CreateProfileInput{
		Key:                "test.usage-isolated",
		PrivacyLevel:       "internal",
		PreferredModelRefs: []string{"local-echo/echo-1"},
	})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if _, err := aiSvc.Chat(ctx, acA, profile.Key, []providers.Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("Chat: %v", err)
	}

	recordsB, err := aiSvc.ListUsage(ctx, acB, ai.UsageFilter{Limit: 50})
	if err != nil {
		t.Fatalf("ListUsage (org B): %v", err)
	}
	if len(recordsB) != 0 {
		t.Fatalf("expected org B to see zero usage records, got %d", len(recordsB))
	}
}

// ListUsage's ProfileKey/ProviderKey filters isolate one profile's or
// provider's usage from another's — an org running several AI
// profiles/providers couldn't otherwise separate them without paging
// through everything.
func TestAIListUsageFiltersByProfileAndProvider(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "ai-usage-filter-owner@nodera.dev")
	h.grantPlatformPermission(t, ctx, ac.ActorID, "platform.ai.providers.manage")
	h.grantPlatformPermission(t, ctx, ac.ActorID, "platform.ai.models.manage")

	mockOllama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"message":           map[string]string{"role": "assistant", "content": "hi from mock ollama"},
			"done":              true,
			"prompt_eval_count": 3,
			"eval_count":        5,
		})
	}))
	defer mockOllama.Close()

	aiSvc := ai.New(pool, h.audit, h.platform, localecho.New(), ollama.New("ollama", mockOllama.URL))

	if _, err := aiSvc.UpsertProvider(ctx, ac, ai.UpsertProviderInput{
		Key: "ollama", Kind: "local", DisplayName: "Ollama (test)", Status: "active",
	}); err != nil {
		t.Fatalf("UpsertProvider: %v", err)
	}
	if _, err := aiSvc.UpsertModel(ctx, ac, ai.UpsertModelInput{
		ProviderKey: "ollama", ModelIdentifier: "llama3.2", Status: "available",
	}); err != nil {
		t.Fatalf("UpsertModel: %v", err)
	}

	echoProfile, err := aiSvc.CreateProfile(ctx, ac, ai.CreateProfileInput{
		Key: "test.usage-filter-echo", PrivacyLevel: "internal", PreferredModelRefs: []string{"local-echo/echo-1"},
	})
	if err != nil {
		t.Fatalf("CreateProfile (echo): %v", err)
	}
	ollamaProfile, err := aiSvc.CreateProfile(ctx, ac, ai.CreateProfileInput{
		Key: "test.usage-filter-ollama", PrivacyLevel: "internal", PreferredModelRefs: []string{"ollama/llama3.2"},
	})
	if err != nil {
		t.Fatalf("CreateProfile (ollama): %v", err)
	}

	if _, err := aiSvc.Chat(ctx, ac, echoProfile.Key, []providers.Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("Chat (echo): %v", err)
	}
	if _, err := aiSvc.Chat(ctx, ac, ollamaProfile.Key, []providers.Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("Chat (ollama): %v", err)
	}

	byProfile, err := aiSvc.ListUsage(ctx, ac, ai.UsageFilter{ProfileKey: echoProfile.Key, Limit: 50})
	if err != nil {
		t.Fatalf("ListUsage (by profile): %v", err)
	}
	if len(byProfile) != 1 || byProfile[0].ProfileKey != echoProfile.Key {
		t.Fatalf("expected exactly the echo profile's usage record, got %+v", byProfile)
	}

	byProvider, err := aiSvc.ListUsage(ctx, ac, ai.UsageFilter{ProviderKey: "ollama", Limit: 50})
	if err != nil {
		t.Fatalf("ListUsage (by provider): %v", err)
	}
	if len(byProvider) != 1 || byProvider[0].ProviderKey != "ollama" {
		t.Fatalf("expected exactly the ollama provider's usage record, got %+v", byProvider)
	}

	all, err := aiSvc.ListUsage(ctx, ac, ai.UsageFilter{Limit: 50})
	if err != nil {
		t.Fatalf("ListUsage (no filter): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected both records with no filter, got %d", len(all))
	}
}

func TestAIListUsageRequiresAIUsePermission(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "ai-usage-perm-owner@nodera.dev")
	h.grantPlatformPermission(t, ctx, ac.ActorID, "platform.ai.providers.manage")
	h.grantPlatformPermission(t, ctx, ac.ActorID, "platform.ai.models.manage")

	aiSvc := ai.New(pool, h.audit, h.platform, localecho.New())

	// The seeded 'member' role already holds ai.use, so a plain member
	// can't exercise this guard — construct a caller with an empty
	// permission set directly, the same as any other permission-guard
	// test needs a caller who genuinely lacks the permission in question.
	noPermsAC := ac
	noPermsAC.Permissions = map[string]struct{}{}

	if _, err := aiSvc.ListUsage(ctx, noPermsAC, ai.UsageFilter{Limit: 50}); err == nil {
		t.Fatal("expected a caller without ai.use to be forbidden from listing usage")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}
}

func TestAIChatUnknownProfileIsNotFound(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "ai-owner-2@nodera.dev")
	h.grantPlatformPermission(t, ctx, ac.ActorID, "platform.ai.providers.manage")
	h.grantPlatformPermission(t, ctx, ac.ActorID, "platform.ai.models.manage")

	aiSvc := ai.New(pool, h.audit, h.platform, localecho.New())

	if _, err := aiSvc.Chat(ctx, ac, "does.not.exist", []providers.Message{{Role: "user", Content: "x"}}); err == nil {
		t.Fatal("expected an error for a nonexistent AI profile")
	}
}

func TestAIUpdateProfileChangesOnlyProvidedFields(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "ai-update-owner@nodera.dev")
	h.grantPlatformPermission(t, ctx, ac.ActorID, "platform.ai.providers.manage")
	h.grantPlatformPermission(t, ctx, ac.ActorID, "platform.ai.models.manage")

	aiSvc := ai.New(pool, h.audit, h.platform, localecho.New())

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
	h.grantPlatformPermission(t, ctx, ac.ActorID, "platform.ai.providers.manage")
	h.grantPlatformPermission(t, ctx, ac.ActorID, "platform.ai.models.manage")

	aiSvc := ai.New(pool, h.audit, h.platform, localecho.New())

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
	h.grantPlatformPermission(t, ctx, ac.ActorID, "platform.ai.providers.manage")
	h.grantPlatformPermission(t, ctx, ac.ActorID, "platform.ai.models.manage")

	aiSvc := ai.New(pool, h.audit, h.platform, localecho.New())

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
	h.grantPlatformPermission(t, ctx, ac.ActorID, "platform.ai.providers.manage")
	h.grantPlatformPermission(t, ctx, ac.ActorID, "platform.ai.models.manage")

	aiSvc := ai.New(pool, h.audit, h.platform, localecho.New())

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
