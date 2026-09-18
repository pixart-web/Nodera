// Package openai implements a second cloud AI provider adapter, talking to
// the OpenAI Chat Completions API
// (https://api.openai.com/v1/chat/completions). It follows the same shape
// as internal/ai/providers/anthropic: a real, production-capable Provider,
// tested against a mock HTTP server rather than a live account (rule 39 —
// no external credential required for development or CI).
//
// Unlike Anthropic, OpenAI's API takes a "system"-role message directly in
// the messages array — no extraction/lifting needed, which makes this
// adapter's Chat noticeably simpler than the Anthropic one.
//
// Like Anthropic, this needs a real secret (an API key) — see the
// credential-handling note in cmd/server/main.go and
// docs/AI_ARCHITECTURE.md for why it's sourced from an env var rather than
// internal/secrets in this phase.
package openai

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

const defaultBaseURL = "https://api.openai.com"

type Provider struct {
	key        string
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// New constructs an OpenAI provider adapter. key is the ai_providers.key
// this adapter answers for — usually "openai".
func New(key, apiKey string) *Provider {
	return NewWithBaseURL(key, apiKey, defaultBaseURL)
}

// NewWithBaseURL is New with an overridable base URL, so tests can point
// the adapter at a mock server instead of api.openai.com (rule 39).
func NewWithBaseURL(key, apiKey, baseURL string) *Provider {
	return &Provider{
		key:        key,
		apiKey:     apiKey,
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 2 * time.Minute},
	}
}

func (p *Provider) Key() string { return p.key }

type openaiMessage struct {
	Role    string `json:"role"` // "system" | "user" | "assistant"
	Content string `json:"content"`
}

type openaiRequest struct {
	Model       string          `json:"model"`
	Messages    []openaiMessage `json:"messages"`
	Temperature float64         `json:"temperature,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
}

type openaiResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

type openaiErrorResponse struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// Chat converts Nodera's normalized request into the OpenAI Chat
// Completions API shape. Unlike Anthropic, a "system"-role message is
// passed straight through in the messages array — OpenAI's API accepts it
// there directly.
func (p *Provider) Chat(ctx context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	messages := make([]openaiMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		messages = append(messages, openaiMessage{Role: m.Role, Content: m.Content})
	}

	body, err := json.Marshal(openaiRequest{
		Model:       req.Model,
		Messages:    messages,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	})
	if err != nil {
		return providers.ChatResponse{}, fmt.Errorf("openai: failed to encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return providers.ChatResponse{}, fmt.Errorf("openai: failed to build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return providers.ChatResponse{}, fmt.Errorf("openai: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return providers.ChatResponse{}, fmt.Errorf("openai: failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp openaiErrorResponse
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error.Message != "" {
			return providers.ChatResponse{}, fmt.Errorf("openai: server returned %d (%s): %s", resp.StatusCode, errResp.Error.Type, errResp.Error.Message)
		}
		return providers.ChatResponse{}, fmt.Errorf("openai: server returned %d", resp.StatusCode)
	}

	var chatResp openaiResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return providers.ChatResponse{}, fmt.Errorf("openai: failed to decode response: %w", err)
	}
	if len(chatResp.Choices) == 0 {
		return providers.ChatResponse{}, fmt.Errorf("openai: response had no choices")
	}

	return providers.ChatResponse{
		Content:      chatResp.Choices[0].Message.Content,
		InputTokens:  chatResp.Usage.PromptTokens,
		OutputTokens: chatResp.Usage.CompletionTokens,
	}, nil
}
