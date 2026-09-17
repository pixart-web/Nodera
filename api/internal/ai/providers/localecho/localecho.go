// Package localecho implements a deterministic, non-fake test provider
// (ADR-006). It is never presented as a production model — its whole
// purpose is to give the router, gateway, and integration tests something
// real to exercise without depending on network access or a vendor
// credential. It is seeded as the 'local-echo' provider / 'echo-1' model in
// migration 0006_ai.sql.
package localecho

import (
	"context"
	"strings"

	"github.com/nodera/nodera/internal/ai/providers"
)

const Key = "local-echo"

type Provider struct{}

func New() *Provider { return &Provider{} }

func (p *Provider) Key() string { return Key }

// Chat deterministically echoes the last user message back, prefixed, so
// callers and tests can assert on exact output. Token counts are a simple
// word-count approximation — real providers report actual tokenization;
// this one intentionally does not pretend to.
func (p *Provider) Chat(ctx context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	select {
	case <-ctx.Done():
		return providers.ChatResponse{}, ctx.Err()
	default:
	}

	var last string
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			last = req.Messages[i].Content
			break
		}
	}

	content := "echo: " + last
	return providers.ChatResponse{
		Content:      content,
		InputTokens:  wordCount(last),
		OutputTokens: wordCount(content),
	}, nil
}

func wordCount(s string) int {
	return len(strings.Fields(s))
}
