package reviewer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// AnthropicClient is the production LLM transport, talking to the
// Anthropic Messages API over HTTPS. It is the only built-in
// Client implementation; tests inject their own.
//
// Configuration is intentionally narrow: api key, optional base
// URL override (for recording / mocking), and an *http.Client with
// a sane default timeout. The substrate has no opinion about
// rate-limit handling — at the typical review cadence (one call
// per story write) it is not needed.
type AnthropicClient struct {
	APIKey  string
	BaseURL string
	HTTP    *http.Client
}

// NewAnthropicClient returns a client configured against the
// public Anthropic endpoint, with a 30s default timeout.
func NewAnthropicClient(apiKey string) *AnthropicClient {
	return &AnthropicClient{
		APIKey:  apiKey,
		BaseURL: "https://api.anthropic.com/v1",
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicResponse struct {
	Content []anthropicContentBlock `json:"content"`
	Error   *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Complete sends one Messages call. The reviewer prompt is the
// sole user message; Anthropic's system field is empty because
// the rubric + output schema already live inline in the prompt
// body (configuration-over-code — moving it to `system` would
// silently change the contract operators see in the markdown).
func (c *AnthropicClient) Complete(ctx context.Context, model string, maxTokens int, prompt string) (string, error) {
	if strings.TrimSpace(c.APIKey) == "" {
		return "", fmt.Errorf("anthropic: api key not configured")
	}
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	body, _ := json.Marshal(anthropicRequest{
		Model:     model,
		MaxTokens: maxTokens,
		Messages:  []anthropicMessage{{Role: "user", Content: prompt}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("anthropic: new request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("anthropic: do: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("anthropic: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("anthropic: http %d: %s", resp.StatusCode, string(respBody))
	}
	var parsed anthropicResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("anthropic: unmarshal: %w", err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("anthropic: %s: %s", parsed.Error.Type, parsed.Error.Message)
	}
	var b strings.Builder
	for _, blk := range parsed.Content {
		if blk.Type == "text" {
			b.WriteString(blk.Text)
		}
	}
	return b.String(), nil
}
