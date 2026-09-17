package integration_test

import (
	"context"
	"testing"

	"github.com/nodera/nodera/internal/ai"
	"github.com/nodera/nodera/internal/ai/providers"
	"github.com/nodera/nodera/internal/ai/providers/localecho"
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
