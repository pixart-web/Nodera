package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nodera/nodera/internal/ai"
	"github.com/nodera/nodera/internal/ai/providers"
	"github.com/nodera/nodera/internal/ai/providers/anthropic"
	"github.com/nodera/nodera/internal/ai/providers/localecho"
	"github.com/nodera/nodera/internal/testhelpers"
)

// Exercises the full pipeline — AI profile → deterministic router →
// provider registry → the real Anthropic adapter — against a mock server
// (httptest), proving the adapter is correctly wired end to end without
// depending on a real Anthropic account or API key (rule 39). Mirrors
// TestAIChatRoutesToOllamaProvider in ollama_integration_test.go.
func TestAIChatRoutesToAnthropicProvider(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "anthropic-owner@nodera.dev")

	mockAnthropic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{{"type": "text", "text": "hello from mock claude"}},
			"usage":   map[string]int{"input_tokens": 4, "output_tokens": 6},
		})
	}))
	defer mockAnthropic.Close()

	aiSvc := ai.New(pool, h.audit, localecho.New(), anthropic.NewWithBaseURL("anthropic", "test-api-key", mockAnthropic.URL))

	// Register the provider in the platform-wide registry (this is what
	// cmd/server/main.go does automatically when NODERA_ANTHROPIC_API_KEY
	// is set) and a model for it. kind: cloud is what makes the RESTRICTED
	// privacy test below meaningful.
	if _, err := aiSvc.UpsertProvider(ctx, ac, ai.UpsertProviderInput{
		Key: "anthropic", Kind: "cloud", DisplayName: "Anthropic (test)", Status: "active",
	}); err != nil {
		t.Fatalf("UpsertProvider: %v", err)
	}
	if _, err := aiSvc.UpsertModel(ctx, ac, ai.UpsertModelInput{
		ProviderKey: "anthropic", ModelIdentifier: "claude-opus-5", Capabilities: []string{"chat"}, Status: "available",
	}); err != nil {
		t.Fatalf("UpsertModel: %v", err)
	}

	profile, err := aiSvc.CreateProfile(ctx, ac, ai.CreateProfileInput{
		Key:                "test.anthropic",
		PrivacyLevel:       "internal",
		PreferredModelRefs: []string{"anthropic/claude-opus-5"},
	})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	result, err := aiSvc.Chat(ctx, ac, profile.Key, []providers.Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if result.Content != "hello from mock claude" {
		t.Fatalf("unexpected content: %q", result.Content)
	}
	if result.ProviderKey != "anthropic" || result.Model != "claude-opus-5" {
		t.Fatalf("expected routing to anthropic/claude-opus-5, got provider=%s model=%s", result.ProviderKey, result.Model)
	}
	if result.InputTokens != 4 || result.OutputTokens != 6 {
		t.Fatalf("unexpected token counts: in=%d out=%d", result.InputTokens, result.OutputTokens)
	}
}

// A RESTRICTED profile must never resolve to Anthropic (kind: cloud),
// regardless of what the profile's preferred_model_ids ask for — the
// privacy policy is enforced in the router, not left to the caller. This
// is the cloud-provider-specific case of the same rule already proven for
// a hypothetical unregistered cloud provider in ollama_integration_test.go's
// TestAIRegistryProviderWithNoAdapterIsUnavailable — here the adapter DOES
// exist and IS registered, and the router still must refuse it.
func TestAIRestrictedProfileNeverRoutesToAnthropic(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "anthropic-restricted-owner@nodera.dev")

	mockAnthropic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("a RESTRICTED profile must never reach the cloud provider's HTTP endpoint at all")
	}))
	defer mockAnthropic.Close()

	aiSvc := ai.New(pool, h.audit, anthropic.NewWithBaseURL("anthropic", "test-api-key", mockAnthropic.URL))

	if _, err := aiSvc.UpsertProvider(ctx, ac, ai.UpsertProviderInput{
		Key: "anthropic", Kind: "cloud", DisplayName: "Anthropic (test)", Status: "active",
	}); err != nil {
		t.Fatalf("UpsertProvider: %v", err)
	}
	if _, err := aiSvc.UpsertModel(ctx, ac, ai.UpsertModelInput{
		ProviderKey: "anthropic", ModelIdentifier: "claude-opus-5", Status: "available",
	}); err != nil {
		t.Fatalf("UpsertModel: %v", err)
	}

	profile, err := aiSvc.CreateProfile(ctx, ac, ai.CreateProfileInput{
		Key:                "test.restricted-anthropic",
		PrivacyLevel:       "restricted",
		PreferredModelRefs: []string{"anthropic/claude-opus-5"},
	})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	if _, err := aiSvc.Chat(ctx, ac, profile.Key, []providers.Message{{Role: "user", Content: "hi"}}); err == nil {
		t.Fatal("expected a RESTRICTED profile to fail rather than route to a cloud provider")
	}
}
