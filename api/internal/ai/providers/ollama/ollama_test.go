package ollama_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nodera/nodera/internal/ai/providers"
	"github.com/nodera/nodera/internal/ai/providers/ollama"
)

// These tests exercise the adapter's HTTP contract against a mock server,
// deliberately not a real Ollama instance — the wire format is stable and
// documented, and tests should not depend on external services or large
// model downloads (rule 39: don't require GPU hardware or huge models for
// normal development).

func TestChat_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode request body: %v", err)
		}
		if body["model"] != "llama3.2" {
			t.Errorf("unexpected model: %v", body["model"])
		}
		if body["stream"] != false {
			t.Errorf("expected stream=false, got %v", body["stream"])
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"message":           map[string]string{"role": "assistant", "content": "hello from ollama"},
			"done":              true,
			"prompt_eval_count": 7,
			"eval_count":        4,
		})
	}))
	defer srv.Close()

	p := ollama.New("ollama", srv.URL)
	resp, err := p.Chat(context.Background(), providers.ChatRequest{
		Model:       "llama3.2",
		Messages:    []providers.Message{{Role: "user", Content: "hi"}},
		Temperature: 0.2,
		MaxTokens:   512,
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "hello from ollama" {
		t.Errorf("unexpected content: %q", resp.Content)
	}
	if resp.InputTokens != 7 || resp.OutputTokens != 4 {
		t.Errorf("unexpected token counts: in=%d out=%d", resp.InputTokens, resp.OutputTokens)
	}
	if p.Key() != "ollama" {
		t.Errorf("unexpected key: %s", p.Key())
	}
}

func TestChat_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": `model "llama3.2" not found`})
	}))
	defer srv.Close()

	p := ollama.New("ollama", srv.URL)
	_, err := p.Chat(context.Background(), providers.ChatRequest{
		Model:    "llama3.2",
		Messages: []providers.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected an error for a 404 response")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected the server's error message to be surfaced, got: %v", err)
	}
}

func TestChat_MalformedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	p := ollama.New("ollama", srv.URL)
	_, err := p.Chat(context.Background(), providers.ChatRequest{
		Model:    "llama3.2",
		Messages: []providers.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected an error for a malformed response body")
	}
}

func TestChat_RespectsContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // block until the client cancels
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := ollama.New("ollama", srv.URL)
	_, err := p.Chat(ctx, providers.ChatRequest{
		Model:    "llama3.2",
		Messages: []providers.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected an error for an already-cancelled context")
	}
}
