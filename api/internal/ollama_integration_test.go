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
	"github.com/nodera/nodera/internal/ai/providers/ollama"
	"github.com/nodera/nodera/internal/testhelpers"
)

// Exercises the full pipeline — AI profile → deterministic router →
// provider registry → the real Ollama adapter — against a mock Ollama
// server (httptest), proving the adapter is correctly wired end to end
// without depending on a real Ollama install (rule 39).
func TestAIChatRoutesToOllamaProvider(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "ollama-owner@nodera.dev")

	mockOllama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"message":           map[string]string{"role": "assistant", "content": "hello from mock ollama"},
			"done":              true,
			"prompt_eval_count": 3,
			"eval_count":        5,
		})
	}))
	defer mockOllama.Close()

	aiSvc := ai.New(pool, h.audit, localecho.New(), ollama.New("ollama", mockOllama.URL))

	// Register the provider in the platform-wide registry (this is what
	// cmd/server/main.go does automatically when NODERA_OLLAMA_BASE_URL is
	// set) and a model for it.
	if _, err := aiSvc.UpsertProvider(ctx, ac, ai.UpsertProviderInput{
		Key: "ollama", Kind: "local", DisplayName: "Ollama (test)", Status: "active",
	}); err != nil {
		t.Fatalf("UpsertProvider: %v", err)
	}
	if _, err := aiSvc.UpsertModel(ctx, ac, ai.UpsertModelInput{
		ProviderKey: "ollama", ModelIdentifier: "llama3.2", Capabilities: []string{"chat"}, Status: "available",
	}); err != nil {
		t.Fatalf("UpsertModel: %v", err)
	}

	profile, err := aiSvc.CreateProfile(ctx, ac, ai.CreateProfileInput{
		Key:                "test.ollama",
		PrivacyLevel:       "internal",
		PreferredModelRefs: []string{"ollama/llama3.2"},
	})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	result, err := aiSvc.Chat(ctx, ac, profile.Key, []providers.Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if result.Content != "hello from mock ollama" {
		t.Fatalf("unexpected content: %q", result.Content)
	}
	if result.ProviderKey != "ollama" || result.Model != "llama3.2" {
		t.Fatalf("expected routing to ollama/llama3.2, got provider=%s model=%s", result.ProviderKey, result.Model)
	}
	if result.InputTokens != 3 || result.OutputTokens != 5 {
		t.Fatalf("unexpected token counts: in=%d out=%d", result.InputTokens, result.OutputTokens)
	}
}

// A provider row can exist in the registry (e.g. added via the API by an
// operator) with no Go adapter registered for it — the router must treat
// it as unavailable, never silently falling back to a different provider
// or fabricating a response (rule 36).
func TestAIRegistryProviderWithNoAdapterIsUnavailable(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "no-adapter-owner@nodera.dev")

	// Note: no 'openai' Go adapter passed to ai.New.
	aiSvc := ai.New(pool, h.audit, localecho.New())

	if _, err := aiSvc.UpsertProvider(ctx, ac, ai.UpsertProviderInput{
		Key: "openai", Kind: "cloud", DisplayName: "OpenAI", Status: "active",
	}); err != nil {
		t.Fatalf("UpsertProvider: %v", err)
	}
	if _, err := aiSvc.UpsertModel(ctx, ac, ai.UpsertModelInput{
		ProviderKey: "openai", ModelIdentifier: "gpt-4", Status: "available",
	}); err != nil {
		t.Fatalf("UpsertModel: %v", err)
	}

	profile, err := aiSvc.CreateProfile(ctx, ac, ai.CreateProfileInput{
		Key:                "test.no-adapter",
		PrivacyLevel:       "internal",
		PreferredModelRefs: []string{"openai/gpt-4"},
	})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	if _, err := aiSvc.Chat(ctx, ac, profile.Key, []providers.Message{{Role: "user", Content: "hi"}}); err == nil {
		t.Fatal("expected Chat to fail when the referenced provider has no registered Go adapter")
	}
}

func TestAIRegistryListProvidersAndModels(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "registry-owner@nodera.dev")

	aiSvc := ai.New(pool, h.audit, localecho.New())

	providersList, err := aiSvc.ListProviders(ctx, ac)
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	found := false
	for _, p := range providersList {
		if p.Key == "local-echo" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected the seeded local-echo provider to appear in ListProviders")
	}

	modelsList, err := aiSvc.ListModels(ctx, ac)
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	found = false
	for _, m := range modelsList {
		if m.ProviderKey == "local-echo" && m.ModelIdentifier == "echo-1" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected the seeded echo-1 model to appear in ListModels")
	}

	// Registering a model against a nonexistent provider is rejected, not
	// silently accepted.
	if _, err := aiSvc.UpsertModel(ctx, ac, ai.UpsertModelInput{
		ProviderKey: "does-not-exist", ModelIdentifier: "x",
	}); err == nil {
		t.Fatal("expected UpsertModel to fail for an unregistered provider_key")
	}
}
