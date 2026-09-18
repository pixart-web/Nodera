package openai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nodera/nodera/internal/ai/providers"
	"github.com/nodera/nodera/internal/ai/providers/openai"
)

// These tests exercise the adapter's HTTP contract against a mock server —
// deliberately not a real OpenAI account. No test in this file, or
// anywhere in the codebase, depends on an OpenAI API key being set (rule
// 39: no external credential required for development or CI).

func TestChat_Success(t *testing.T) {
	var capturedBody map[string]any
	var capturedHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&capturedBody); err != nil {
			t.Fatalf("failed to decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": "hello from gpt"}}},
			"usage":   map[string]int{"prompt_tokens": 5, "completion_tokens": 3},
		})
	}))
	defer srv.Close()

	p := openai.NewWithBaseURL("openai", "test-api-key", srv.URL)
	resp, err := p.Chat(context.Background(), providers.ChatRequest{
		Model: "gpt-5",
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
	if resp.Content != "hello from gpt" {
		t.Errorf("unexpected content: %q", resp.Content)
	}
	if resp.InputTokens != 5 || resp.OutputTokens != 3 {
		t.Errorf("unexpected token counts: in=%d out=%d", resp.InputTokens, resp.OutputTokens)
	}
	if p.Key() != "openai" {
		t.Errorf("unexpected key: %s", p.Key())
	}

	if capturedHeaders.Get("Authorization") != "Bearer test-api-key" {
		t.Errorf("expected Authorization header to be set, got %q", capturedHeaders.Get("Authorization"))
	}

	// Unlike Anthropic, the system-role message stays in the messages
	// array — OpenAI's API accepts it there directly.
	msgs, ok := capturedBody["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("expected both messages (system + user) to pass through unchanged, got %v", capturedBody["messages"])
	}
	first := msgs[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "You are a helpful assistant." {
		t.Errorf("unexpected first message: %v", first)
	}
}

func TestChat_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"type": "rate_limit_exceeded", "message": "rate limited, slow down"},
		})
	}))
	defer srv.Close()

	p := openai.NewWithBaseURL("openai", "test-api-key", srv.URL)
	_, err := p.Chat(context.Background(), providers.ChatRequest{
		Model:    "gpt-5",
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

	p := openai.NewWithBaseURL("openai", "test-api-key", srv.URL)
	_, err := p.Chat(context.Background(), providers.ChatRequest{
		Model:    "gpt-5",
		Messages: []providers.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected an error for a malformed response body")
	}
}

// A response with an empty choices array is a legitimate, if unusual, API
// response shape — the adapter must report it as an error rather than
// returning a zero-value ChatResponse that looks like an empty-but-valid
// reply (rule 36: never fabricate a result).
func TestChat_EmptyChoicesIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{},
			"usage":   map[string]int{},
		})
	}))
	defer srv.Close()

	p := openai.NewWithBaseURL("openai", "test-api-key", srv.URL)
	_, err := p.Chat(context.Background(), providers.ChatRequest{
		Model:    "gpt-5",
		Messages: []providers.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected an error when the response has no choices")
	}
}

func TestChat_RespectsContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := openai.NewWithBaseURL("openai", "test-api-key", srv.URL)
	_, err := p.Chat(ctx, providers.ChatRequest{
		Model:    "gpt-5",
		Messages: []providers.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected an error for an already-cancelled context")
	}
}
