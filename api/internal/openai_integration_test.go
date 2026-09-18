package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nodera/nodera/internal/ai"
	"github.com/nodera/nodera/internal/ai/providers"
	"github.com/nodera/nodera/internal/ai/providers/localecho"
	"github.com/nodera/nodera/internal/ai/providers/openai"
	"github.com/nodera/nodera/internal/testhelpers"
)

// Exercises the full pipeline — AI profile → deterministic router →
// provider registry → the real OpenAI adapter — against a mock server
// (httptest), proving the adapter is correctly wired end to end without
// depending on a real OpenAI account or API key (rule 39). Mirrors
// TestAIChatRoutesToAnthropicProvider in anthropic_integration_test.go.
func TestAIChatRoutesToOpenAIProvider(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "openai-owner@nodera.dev")

	mockOpenAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": "hello from mock gpt"}}},
			"usage":   map[string]int{"prompt_tokens": 4, "completion_tokens": 6},
		})
	}))
	defer mockOpenAI.Close()

	aiSvc := ai.New(pool, h.audit, localecho.New(), openai.NewWithBaseURL("openai", "test-api-key", mockOpenAI.URL))

	// Register the provider in the platform-wide registry (this is what
	// cmd/server/main.go does automatically when NODERA_OPENAI_API_KEY is
	// set) and a model for it. kind: cloud is what makes the RESTRICTED
	// privacy test below meaningful.
	if _, err := aiSvc.UpsertProvider(ctx, ac, ai.UpsertProviderInput{
		Key: "openai", Kind: "cloud", DisplayName: "OpenAI (test)", Status: "active",
	}); err != nil {
		t.Fatalf("UpsertProvider: %v", err)
	}
	if _, err := aiSvc.UpsertModel(ctx, ac, ai.UpsertModelInput{
		ProviderKey: "openai", ModelIdentifier: "gpt-5", Capabilities: []string{"chat"}, Status: "available",
	}); err != nil {
		t.Fatalf("UpsertModel: %v", err)
	}

	profile, err := aiSvc.CreateProfile(ctx, ac, ai.CreateProfileInput{
		Key:                "test.openai",
		PrivacyLevel:       "internal",
		PreferredModelRefs: []string{"openai/gpt-5"},
	})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	result, err := aiSvc.Chat(ctx, ac, profile.Key, []providers.Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if result.Content != "hello from mock gpt" {
		t.Fatalf("unexpected content: %q", result.Content)
	}
	if result.ProviderKey != "openai" || result.Model != "gpt-5" {
		t.Fatalf("expected routing to openai/gpt-5, got provider=%s model=%s", result.ProviderKey, result.Model)
	}
	if result.InputTokens != 4 || result.OutputTokens != 6 {
		t.Fatalf("unexpected token counts: in=%d out=%d", result.InputTokens, result.OutputTokens)
	}
}

// A RESTRICTED profile must never resolve to OpenAI (kind: cloud),
// regardless of what the profile's preferred_model_ids ask for — the
// privacy policy is enforced in the router, not left to the caller. Same
// case already proven for Anthropic in
// TestAIRestrictedProfileNeverRoutesToAnthropic.
func TestAIRestrictedProfileNeverRoutesToOpenAI(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "openai-restricted-owner@nodera.dev")

	mockOpenAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("a RESTRICTED profile must never reach the cloud provider's HTTP endpoint at all")
	}))
	defer mockOpenAI.Close()

	aiSvc := ai.New(pool, h.audit, openai.NewWithBaseURL("openai", "test-api-key", mockOpenAI.URL))

	if _, err := aiSvc.UpsertProvider(ctx, ac, ai.UpsertProviderInput{
		Key: "openai", Kind: "cloud", DisplayName: "OpenAI (test)", Status: "active",
	}); err != nil {
		t.Fatalf("UpsertProvider: %v", err)
	}
	if _, err := aiSvc.UpsertModel(ctx, ac, ai.UpsertModelInput{
		ProviderKey: "openai", ModelIdentifier: "gpt-5", Status: "available",
	}); err != nil {
		t.Fatalf("UpsertModel: %v", err)
	}

	profile, err := aiSvc.CreateProfile(ctx, ac, ai.CreateProfileInput{
		Key:                "test.restricted-openai",
		PrivacyLevel:       "restricted",
		PreferredModelRefs: []string{"openai/gpt-5"},
	})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	if _, err := aiSvc.Chat(ctx, ac, profile.Key, []providers.Message{{Role: "user", Content: "hi"}}); err == nil {
		t.Fatal("expected a RESTRICTED profile to fail rather than route to a cloud provider")
	}
}
