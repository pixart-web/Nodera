// Package providers defines Nodera's normalized AI provider interface
// (section 10). Every provider — local or cloud — implements this same
// shape so the router and callers never see vendor-specific request/response
// types (rule: never leak provider-specific details throughout the app).
package providers

import "context"

type Message struct {
	Role    string `json:"role"` // "system" | "user" | "assistant"
	Content string `json:"content"`
}

type ChatRequest struct {
	Model       string // provider's own model identifier
	Messages    []Message
	Temperature float64
	MaxTokens   int
}

type ChatResponse struct {
	Content      string
	InputTokens  int
	OutputTokens int
}

// Provider is Nodera's normalized AI provider adapter interface. A concrete
// provider (OpenAI, Anthropic, Ollama, ...) implements this and is
// registered with the AI service at process wiring time (cmd/server) — the
// router only ever depends on this interface, never a vendor SDK type.
type Provider interface {
	// Key must match the `key` column of the provider's ai_providers row.
	Key() string
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
}
