package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// GeminiAgentModel implements AgentModel against Gemini generateContent with
// function calling: it declares the tools, parses functionCall parts as tool
// calls, and replays functionResponse turns. The harness owns the loop; this
// only renders one model turn. NEVER constructed client-side (Gemini is the
// server executor; claude -p stays the client's).
type GeminiAgentModel struct {
	apiKey string
	model  string
	client *http.Client
}

// NewGeminiAgentModel builds the model; an empty model falls back to the default.
func NewGeminiAgentModel(apiKey, model string) *GeminiAgentModel {
	if strings.TrimSpace(model) == "" {
		model = "gemini-2.5-flash"
	}
	return &GeminiAgentModel{apiKey: apiKey, model: model, client: &http.Client{Timeout: 120 * time.Second}}
}

type geminiFunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type geminiFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiFunctionDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type geminiToolDecl struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations"`
}

type geminiAgentReq struct {
	SystemInstruction *geminiContent   `json:"systemInstruction,omitempty"`
	Contents          []geminiContent  `json:"contents"`
	Tools             []geminiToolDecl `json:"tools,omitempty"`
}

type geminiAgentResp struct {
	Candidates []struct {
		Content geminiContent `json:"content"`
	} `json:"candidates"`
}

// Step renders one model turn over the full history (Gemini is stateless).
func (m *GeminiAgentModel) Step(ctx context.Context, system string, history []Turn, tools []Tool) (AgentStep, error) {
	if strings.TrimSpace(m.apiKey) == "" {
		return AgentStep{}, fmt.Errorf("agent: gemini api key not configured")
	}
	reqBody := geminiAgentReq{
		Contents: contentsFromHistory(history),
		Tools:    toolDeclarations(tools),
	}
	if strings.TrimSpace(system) != "" {
		reqBody.SystemInstruction = &geminiContent{Parts: []geminiPart{{Text: system}}}
	}
	respBytes, err := m.post(ctx, reqBody)
	if err != nil {
		return AgentStep{}, err
	}
	var gr geminiAgentResp
	if err := json.Unmarshal(respBytes, &gr); err != nil {
		return AgentStep{}, fmt.Errorf("agent: unmarshal response: %w", err)
	}
	if len(gr.Candidates) == 0 {
		return AgentStep{}, fmt.Errorf("agent: gemini returned no candidates")
	}
	var text strings.Builder
	var calls []ToolCall
	for _, p := range gr.Candidates[0].Content.Parts {
		switch {
		case p.FunctionCall != nil:
			args := p.FunctionCall.Args
			if len(args) == 0 {
				args = json.RawMessage(`{}`)
			}
			calls = append(calls, ToolCall{Name: p.FunctionCall.Name, Args: args})
		case p.Text != "":
			text.WriteString(p.Text)
		}
	}
	if len(calls) > 0 {
		return AgentStep{ToolCalls: calls}, nil
	}
	return AgentStep{Final: strings.TrimSpace(text.String())}, nil
}

func contentsFromHistory(history []Turn) []geminiContent {
	out := make([]geminiContent, 0, len(history))
	for _, t := range history {
		switch t.Role {
		case "model":
			parts := make([]geminiPart, 0, len(t.ToolCalls))
			for _, c := range t.ToolCalls {
				parts = append(parts, geminiPart{FunctionCall: &geminiFunctionCall{Name: c.Name, Args: c.Args}})
			}
			out = append(out, geminiContent{Role: "model", Parts: parts})
		case "tool":
			out = append(out, geminiContent{Role: "user", Parts: []geminiPart{{
				FunctionResponse: &geminiFunctionResponse{Name: t.ToolName, Response: map[string]any{"result": t.ToolResult}},
			}}})
		default: // user
			out = append(out, geminiContent{Role: "user", Parts: []geminiPart{{Text: t.Text}}})
		}
	}
	return out
}

func toolDeclarations(tools []Tool) []geminiToolDecl {
	if len(tools) == 0 {
		return nil
	}
	decls := make([]geminiFunctionDeclaration, 0, len(tools))
	for _, t := range tools {
		decls = append(decls, geminiFunctionDeclaration{Name: t.Name, Description: t.Description, Parameters: t.Parameters})
	}
	return []geminiToolDecl{{FunctionDeclarations: decls}}
}

// post sends one generateContent request with 429 backoff, returning the body.
func (m *GeminiAgentModel) post(ctx context.Context, body geminiAgentReq) ([]byte, error) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("agent: marshal request: %w", err)
	}
	endpoint := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent", m.model)
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, fmt.Errorf("agent: new request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		q := req.URL.Query()
		q.Set("key", m.apiKey)
		req.URL.RawQuery = q.Encode()

		resp, err := m.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("agent: gemini call: %w", err)
		}
		respBytes, rerr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if rerr != nil {
			return nil, fmt.Errorf("agent: read response: %w", rerr)
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < 2 {
			backoff := time.Duration(2<<attempt) * time.Second
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if secs, e := strconv.Atoi(ra); e == nil && secs > 0 {
					backoff = time.Duration(secs) * time.Second
				}
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
				continue
			}
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("agent: gemini api error %d: %s", resp.StatusCode, strings.TrimSpace(string(respBytes)))
		}
		return respBytes, nil
	}
}
