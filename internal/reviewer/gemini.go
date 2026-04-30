package reviewer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// GeminiBaseURL is the canonical Gemini endpoint root. Override via
// GeminiConfig.BaseURL for tests against an httptest server.
const GeminiBaseURL = "https://generativelanguage.googleapis.com"

// DefaultGeminiReviewModel is the default Gemini model used for reviews.
// gemini-2.5-flash gives a good cost/latency tradeoff for the rubric
// volumes we expect; the operator can override via SATELLITES_GEMINI_REVIEW_MODEL.
const DefaultGeminiReviewModel = "gemini-2.5-flash"

const (
	geminiHTTPTimeout = 60 * time.Second
)

// GeminiConfig carries the operator-supplied Gemini reviewer settings.
type GeminiConfig struct {
	APIKey  string
	Model   string
	BaseURL string
}

// GeminiReviewer calls the Gemini generateContent endpoint with the
// rubric + evidence as a single prompt and parses the verdict back from
// the model's JSON response. Safe for concurrent use — every call
// constructs its own request.
type GeminiReviewer struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
}

// NewGeminiReviewer constructs a reviewer against the supplied config.
// Empty Model / BaseURL fall back to the package defaults so callers
// can pass a near-empty config and get production behaviour.
func NewGeminiReviewer(cfg GeminiConfig) *GeminiReviewer {
	if cfg.Model == "" {
		cfg.Model = DefaultGeminiReviewModel
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = GeminiBaseURL
	}
	return &GeminiReviewer{
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
		baseURL: cfg.BaseURL,
		client:  &http.Client{Timeout: geminiHTTPTimeout},
	}
}

// geminiRequest is the generateContent request body.
type geminiRequest struct {
	Contents         []geminiContent      `json:"contents"`
	GenerationConfig *geminiGenerationCfg `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiGenerationCfg struct {
	ResponseMIMEType string   `json:"responseMimeType,omitempty"`
	Temperature      *float32 `json:"temperature,omitempty"`
}

// geminiResponse is the generateContent response shape.
type geminiResponse struct {
	Candidates    []geminiCandidate `json:"candidates"`
	UsageMetadata geminiUsage       `json:"usageMetadata"`
}

type geminiCandidate struct {
	Content geminiContent `json:"content"`
}

type geminiUsage struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

// Review implements Reviewer by sending the rubric + evidence to
// Gemini and parsing the model's structured verdict out of its JSON
// response. Returns a rejected verdict (with rationale) when the
// model output cannot be parsed — never returns nil verdict on a
// successful HTTP call.
func (g *GeminiReviewer) Review(ctx context.Context, req Request) (Verdict, UsageCost, error) {
	prompt := buildGeminiPrompt(req)
	body, err := json.Marshal(geminiRequest{
		Contents: []geminiContent{{Role: "user", Parts: []geminiPart{{Text: prompt}}}},
		GenerationConfig: &geminiGenerationCfg{
			ResponseMIMEType: "application/json",
		},
	})
	if err != nil {
		return Verdict{}, UsageCost{}, fmt.Errorf("marshal gemini request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/v1beta/models/%s:generateContent", g.baseURL, g.model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Verdict{}, UsageCost{}, fmt.Errorf("create gemini request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	q := httpReq.URL.Query()
	q.Set("key", g.apiKey)
	httpReq.URL.RawQuery = q.Encode()

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return Verdict{}, UsageCost{}, fmt.Errorf("gemini api call failed (model=%s)", g.model)
	}
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return Verdict{}, UsageCost{}, fmt.Errorf("read gemini response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Verdict{}, UsageCost{}, fmt.Errorf("gemini api error %d (model=%s)", resp.StatusCode, g.model)
	}

	var decoded geminiResponse
	if err := json.Unmarshal(respBytes, &decoded); err != nil {
		return Verdict{}, UsageCost{}, fmt.Errorf("unmarshal gemini response: %w", err)
	}
	if len(decoded.Candidates) == 0 || len(decoded.Candidates[0].Content.Parts) == 0 {
		return Verdict{
			Outcome:   VerdictRejected,
			Rationale: "gemini returned no candidates",
		}, UsageCost{Model: g.model}, nil
	}

	text := decoded.Candidates[0].Content.Parts[0].Text
	verdict := parseGeminiVerdict(text)

	usage := UsageCost{
		InputTokens:  decoded.UsageMetadata.PromptTokenCount,
		OutputTokens: decoded.UsageMetadata.CandidatesTokenCount,
		Model:        g.model,
	}
	return verdict, usage, nil
}

// buildGeminiPrompt assembles the reviewer prompt from a Request. The
// agent_instruction defines what the contract expected; the rubric
// (reviewer agent body) defines how to judge it; the evidence is the
// agent's submission.
func buildGeminiPrompt(req Request) string {
	var sb strings.Builder
	sb.WriteString("You are the satellites lifecycle reviewer. Read the contract instruction, the reviewer rubric, and the agent's submitted evidence, then return a JSON verdict.\n\n")
	sb.WriteString("Respond with ONLY a JSON object (no fenced block, no commentary) with these fields:\n")
	sb.WriteString("- outcome: one of \"accepted\", \"rejected\", \"needs_more\".\n")
	sb.WriteString("- rationale: short paragraph explaining the verdict; cite principles by id when rejecting.\n")
	sb.WriteString("- principles_cited: list of principle ids you relied on (may be empty).\n")
	sb.WriteString("- review_questions: list of specific questions (only when outcome=needs_more, otherwise empty).\n\n")
	sb.WriteString("=== Contract instruction ===\n")
	sb.WriteString(req.AgentInstruction)
	sb.WriteString("\n\n=== Reviewer rubric ===\n")
	if req.ReviewerRubric != "" {
		sb.WriteString(req.ReviewerRubric)
	} else {
		sb.WriteString("(no rubric configured)")
	}
	if len(req.ACScope) > 0 {
		sb.WriteString("\n\n=== AC scope ===\n")
		sb.WriteString(fmt.Sprintf("Limit AC-coverage check to ACs: %v", req.ACScope))
	}
	sb.WriteString("\n\n=== Evidence ===\n")
	if req.EvidenceMarkdown != "" {
		sb.WriteString(req.EvidenceMarkdown)
	} else {
		sb.WriteString("(no evidence submitted)")
	}
	if len(req.RecentLedger) > 0 {
		sb.WriteString("\n\n=== Recent ledger snippets ===\n")
		for _, snip := range req.RecentLedger {
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", snip.Type, snip.Content))
		}
	}
	return sb.String()
}

// parseGeminiVerdict extracts the structured verdict from the model's
// text response. The prompt asks for a bare JSON object; this fn is
// tolerant of optional fenced blocks and surrounding whitespace.
func parseGeminiVerdict(text string) Verdict {
	jsonText := extractJSONObject(text)
	if jsonText == "" {
		return Verdict{
			Outcome:   VerdictRejected,
			Rationale: "gemini response did not contain a parseable JSON verdict",
		}
	}
	var raw struct {
		Outcome         string   `json:"outcome"`
		Rationale       string   `json:"rationale"`
		PrinciplesCited []string `json:"principles_cited"`
		ReviewQuestions []string `json:"review_questions"`
	}
	if err := json.Unmarshal([]byte(jsonText), &raw); err != nil {
		return Verdict{
			Outcome:   VerdictRejected,
			Rationale: fmt.Sprintf("gemini verdict parse failed: %v", err),
		}
	}
	outcome := strings.TrimSpace(strings.ToLower(raw.Outcome))
	switch outcome {
	case VerdictAccepted, VerdictRejected, VerdictNeedsMore:
		// pass through
	default:
		return Verdict{
			Outcome:   VerdictRejected,
			Rationale: fmt.Sprintf("gemini returned unknown outcome %q", raw.Outcome),
		}
	}
	return Verdict{
		Outcome:         outcome,
		Rationale:       raw.Rationale,
		PrinciplesCited: raw.PrinciplesCited,
		ReviewQuestions: raw.ReviewQuestions,
	}
}

var fencedJSONRe = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")

// extractJSONObject pulls a JSON object out of a model response. Tries
// (a) raw text trimmed; (b) first fenced block; (c) the substring from
// the first '{' to the last '}'. Returns "" when no candidate parses.
func extractJSONObject(text string) string {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
		return trimmed
	}
	if m := fencedJSONRe.FindStringSubmatch(trimmed); len(m) == 2 {
		return strings.TrimSpace(m[1])
	}
	first := strings.Index(trimmed, "{")
	last := strings.LastIndex(trimmed, "}")
	if first >= 0 && last > first {
		return strings.TrimSpace(trimmed[first : last+1])
	}
	return ""
}

// Compile-time assertion.
var _ Reviewer = (*GeminiReviewer)(nil)
