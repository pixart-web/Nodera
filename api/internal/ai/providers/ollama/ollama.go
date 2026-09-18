// Package ollama implements Nodera's first real (non-test) AI provider
// adapter, talking to an Ollama-compatible local inference server's
// /api/chat endpoint. It is deliberately the first production adapter
// because it needs no cloud credential to develop or run against — see
// docs/ROADMAP.md.
package ollama

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

type Provider struct {
	key        string
	baseURL    string
	httpClient *http.Client
}

// New constructs an Ollama provider adapter. key is the ai_providers.key
// this adapter answers for — usually "ollama", but a deployment could run
// more than one Ollama-compatible server (e.g. one per node) under
// different keys, each with its own Provider instance and base URL.
func New(key, baseURL string) *Provider {
	return &Provider{
		key:        key,
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 2 * time.Minute},
	}
}

func (p *Provider) Key() string { return p.key }

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Options  ollamaOptions   `json:"options,omitempty"`
}

type ollamaOptions struct {
	Temperature float64 `json:"temperature,omitempty"`
	NumPredict  int     `json:"num_predict,omitempty"`
}

type ollamaChatResponse struct {
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	Done            bool `json:"done"`
	PromptEvalCount int  `json:"prompt_eval_count"`
	EvalCount       int  `json:"eval_count"`
}

type ollamaErrorResponse struct {
	Error string `json:"error"`
}

func (p *Provider) Chat(ctx context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	messages := make([]ollamaMessage, len(req.Messages))
	for i, m := range req.Messages {
		messages[i] = ollamaMessage{Role: m.Role, Content: m.Content}
	}

	body, err := json.Marshal(ollamaChatRequest{
		Model:    req.Model,
		Messages: messages,
		Stream:   false,
		Options: ollamaOptions{
			Temperature: req.Temperature,
			NumPredict:  req.MaxTokens,
		},
	})
	if err != nil {
		return providers.ChatResponse{}, fmt.Errorf("ollama: failed to encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return providers.ChatResponse{}, fmt.Errorf("ollama: failed to build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return providers.ChatResponse{}, fmt.Errorf("ollama: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return providers.ChatResponse{}, fmt.Errorf("ollama: failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp ollamaErrorResponse
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			return providers.ChatResponse{}, fmt.Errorf("ollama: server returned %d: %s", resp.StatusCode, errResp.Error)
		}
		return providers.ChatResponse{}, fmt.Errorf("ollama: server returned %d", resp.StatusCode)
	}

	var chatResp ollamaChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return providers.ChatResponse{}, fmt.Errorf("ollama: failed to decode response: %w", err)
	}

	return providers.ChatResponse{
		Content:      chatResp.Message.Content,
		InputTokens:  chatResp.PromptEvalCount,
		OutputTokens: chatResp.EvalCount,
	}, nil
}
