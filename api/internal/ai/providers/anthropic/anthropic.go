// Package anthropic implements Nodera's first cloud AI provider adapter,
// talking to the Anthropic Messages API (https://api.anthropic.com/v1/messages).
// It follows the same shape as internal/ai/providers/ollama: a real,
// production-capable Provider, tested against a mock HTTP server rather
// than a live account (rule 39 — no external credential required for
// development or CI).
//
// Unlike Ollama, this genuinely needs a secret (an API key) to be real —
// see the credential-handling note in cmd/server/main.go and
// docs/AI_ARCHITECTURE.md for why it's sourced from an env var rather than
// internal/secrets in this phase.
package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/nodera/nodera/internal/ai/providers"
)

const (
	defaultBaseURL   = "https://api.anthropic.com"
	apiVersion       = "2023-06-01"
	defaultMaxTokens = 1024 // Anthropic requires max_tokens; Nodera profiles may leave it unset
)

type Provider struct {
	key        string
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// New constructs an Anthropic provider adapter. key is the ai_providers.key
// this adapter answers for — usually "anthropic".
func New(key, apiKey string) *Provider {
	return NewWithBaseURL(key, apiKey, defaultBaseURL)
}

// NewWithBaseURL is New with an overridable base URL, so tests can point
// the adapter at a mock server instead of api.anthropic.com (rule 39).
func NewWithBaseURL(key, apiKey, baseURL string) *Provider {
	return &Provider{
		key:        key,
		apiKey:     apiKey,
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 2 * time.Minute},
	}
}

func (p *Provider) Key() string { return p.key }

type anthropicMessage struct {
	Role    string `json:"role"` // "user" | "assistant" — no "system" here, see Chat
	Content string `json:"content"`
}

type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature float64            `json:"temperature,omitempty"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type anthropicErrorResponse struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// Chat converts Nodera's normalized request into the Anthropic Messages API
// shape. Anthropic — unlike Ollama and most OpenAI-compatible APIs — takes
// the system prompt as a separate top-level field, not a "system"-role
// message; any "system" role messages in req.Messages are concatenated
// into that field, in order, rather than being dropped or rejected.
func (p *Provider) Chat(ctx context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	var system string
	messages := make([]anthropicMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == "system" {
			if system != "" {
				system += "\n"
			}
			system += m.Content
			continue
		}
		messages = append(messages, anthropicMessage{Role: m.Role, Content: m.Content})
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	body, err := json.Marshal(anthropicRequest{
		Model:       req.Model,
		MaxTokens:   maxTokens,
		Temperature: req.Temperature,
		System:      system,
		Messages:    messages,
	})
	if err != nil {
		return providers.ChatResponse{}, fmt.Errorf("anthropic: failed to encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return providers.ChatResponse{}, fmt.Errorf("anthropic: failed to build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", apiVersion)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return providers.ChatResponse{}, fmt.Errorf("anthropic: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return providers.ChatResponse{}, fmt.Errorf("anthropic: failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp anthropicErrorResponse
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error.Message != "" {
			return providers.ChatResponse{}, fmt.Errorf("anthropic: server returned %d (%s): %s", resp.StatusCode, errResp.Error.Type, errResp.Error.Message)
		}
		return providers.ChatResponse{}, fmt.Errorf("anthropic: server returned %d", resp.StatusCode)
	}

	var chatResp anthropicResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return providers.ChatResponse{}, fmt.Errorf("anthropic: failed to decode response: %w", err)
	}

	var text string
	for _, block := range chatResp.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}

	return providers.ChatResponse{
		Content:      text,
		InputTokens:  chatResp.Usage.InputTokens,
		OutputTokens: chatResp.Usage.OutputTokens,
	}, nil
}
