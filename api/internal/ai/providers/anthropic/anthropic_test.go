package anthropic_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nodera/nodera/internal/ai/providers"
	"github.com/nodera/nodera/internal/ai/providers/anthropic"
)

// These tests exercise the adapter's HTTP contract against a mock server —
// deliberately not a real Anthropic account. No test in this file, or
// anywhere in the codebase, depends on ANTHROPIC_API_KEY being set (rule
// 39: no external credential required for development or CI).

func TestChat_Success(t *testing.T) {
	var capturedBody map[string]any
	var capturedHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&capturedBody); err != nil {
			t.Fatalf("failed to decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{{"type": "text", "text": "hello from claude"}},
			"usage":   map[string]int{"input_tokens": 5, "output_tokens": 3},
		})
	}))
	defer srv.Close()

	p := anthropic.NewWithBaseURL("anthropic", "test-api-key", srv.URL)
	resp, err := p.Chat(context.Background(), providers.ChatRequest{
		Model: "claude-opus-5",
		Messages: []providers.Message{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "hi"},
		},
		Temperature: 0.2,
		MaxTokens:   512,
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "hello from claude" {
		t.Errorf("unexpected content: %q", resp.Content)
	}
	if resp.InputTokens != 5 || resp.OutputTokens != 3 {
		t.Errorf("unexpected token counts: in=%d out=%d", resp.InputTokens, resp.OutputTokens)
	}
	if p.Key() != "anthropic" {
		t.Errorf("unexpected key: %s", p.Key())
	}

	if capturedHeaders.Get("x-api-key") != "test-api-key" {
		t.Errorf("expected x-api-key header to be set, got %q", capturedHeaders.Get("x-api-key"))
	}
	if capturedHeaders.Get("anthropic-version") == "" {
		t.Error("expected anthropic-version header to be set")
	}

	// The system-role message must be lifted into the top-level "system"
	// field, not left in the messages array (Anthropic rejects a
	// "system"-role message there).
	if capturedBody["system"] != "You are a helpful assistant." {
		t.Errorf("expected system field to carry the system message, got %v", capturedBody["system"])
	}
	msgs, ok := capturedBody["messages"].([]any)
	if !ok || len(msgs) != 1 {
		t.Fatalf("expected exactly 1 message (the user one) after extracting system, got %v", capturedBody["messages"])
	}
	first := msgs[0].(map[string]any)
	if first["role"] != "user" || first["content"] != "hi" {
		t.Errorf("unexpected remaining message: %v", first)
	}
}

func TestChat_MultipleSystemMessagesAreJoined(t *testing.T) {
	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{{"type": "text", "text": "ok"}},
			"usage":   map[string]int{},
		})
	}))
	defer srv.Close()

	p := anthropic.NewWithBaseURL("anthropic", "test-api-key", srv.URL)
	_, err := p.Chat(context.Background(), providers.ChatRequest{
		Model: "claude-opus-5",
		Messages: []providers.Message{
			{Role: "system", Content: "First instruction."},
			{Role: "system", Content: "Second instruction."},
			{Role: "user", Content: "go"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if capturedBody["system"] != "First instruction.\nSecond instruction." {
		t.Errorf("expected joined system messages, got %q", capturedBody["system"])
	}
}

func TestChat_DefaultsMaxTokensWhenUnset(t *testing.T) {
	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{{"type": "text", "text": "ok"}},
			"usage":   map[string]int{},
		})
	}))
	defer srv.Close()

	p := anthropic.NewWithBaseURL("anthropic", "test-api-key", srv.URL)
	// MaxTokens deliberately left at zero — Anthropic's API requires a
	// positive max_tokens, unlike Ollama's optional num_predict.
	_, err := p.Chat(context.Background(), providers.ChatRequest{
		Model:    "claude-opus-5",
		Messages: []providers.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if capturedBody["max_tokens"].(float64) <= 0 {
		t.Errorf("expected a positive default max_tokens, got %v", capturedBody["max_tokens"])
	}
}

func TestChat_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"type": "rate_limit_error", "message": "rate limited, slow down"},
		})
	}))
	defer srv.Close()

	p := anthropic.NewWithBaseURL("anthropic", "test-api-key", srv.URL)
	_, err := p.Chat(context.Background(), providers.ChatRequest{
		Model:    "claude-opus-5",
		Messages: []providers.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected an error for a 429 response")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("expected the server's error message to be surfaced, got: %v", err)
	}
}

func TestChat_MalformedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	p := anthropic.NewWithBaseURL("anthropic", "test-api-key", srv.URL)
	_, err := p.Chat(context.Background(), providers.ChatRequest{
		Model:    "claude-opus-5",
		Messages: []providers.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected an error for a malformed response body")
	}
}

func TestChat_RespectsContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := anthropic.NewWithBaseURL("anthropic", "test-api-key", srv.URL)
	_, err := p.Chat(ctx, providers.ChatRequest{
		Model:    "claude-opus-5",
		Messages: []providers.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected an error for an already-cancelled context")
	}
}
