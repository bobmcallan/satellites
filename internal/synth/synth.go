// Package synth is workspace objective synthesis (sty_a0099c04): a generation
// TASK (the objective prompt over a workspace's corpus) runnable by either
// EXECUTOR — Gemini server-side (generateContent) or `claude -p` client-side —
// chosen by environment. "Configuration over code": the task spec
// (BuildObjectivePrompt) is shared data; the executor is the only difference.
// `claude -p` never runs server-side, preserving the no-server-Claude constraint
// (epic:always-context); the server path uses Gemini.
package synth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
)

// Generator turns a prompt into generated text — the executor seam. GeminiGenerator
// (server) and ClaudeLocalGenerator (client) both satisfy it.
type Generator interface {
	Generate(ctx context.Context, prompt string) (string, error)
}

// CorpusDoc is one workspace document fed into the objective prompt.
type CorpusDoc struct {
	Name string
	Body string
}

// ObjectiveDocName is the reserved workspace document the synthesized objective
// is written to (and excluded from its own corpus).
const ObjectiveDocName = "objective"

// BuildObjectivePrompt is the TASK spec: it renders the corpus into the prompt
// both executors run. Shared data, not executor-specific logic.
func BuildObjectivePrompt(corpus []CorpusDoc) string {
	var b strings.Builder
	b.WriteString("You are assisting a delivery/project manager. Below are the documents collected for a client engagement (a workspace corpus). ")
	b.WriteString("Synthesize a concise engagement OBJECTIVE: 2–4 short paragraphs stating what this engagement is about, its goal, and the current focus — grounded only in the documents. ")
	b.WriteString("Do not invent facts not supported by the documents. Output the objective as plain markdown, no preamble.\n\n")
	b.WriteString("--- DOCUMENTS ---\n")
	for _, d := range corpus {
		b.WriteString("\n## ")
		b.WriteString(d.Name)
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(d.Body))
		b.WriteString("\n")
	}
	b.WriteString("\n--- END DOCUMENTS ---\n")
	return b.String()
}

// ---- Gemini generator (server-side, autonomous) ----

// DefaultGenerationModel is a generateContent-capable Gemini model.
const (
	DefaultGenerationModel = "gemini-2.5-flash"
	geminiBaseURL          = "https://generativelanguage.googleapis.com"
)

// GeminiGenerator calls Gemini's generateContent. Server-side: the autonomous
// executor used when no operator/client is driving.
type GeminiGenerator struct {
	apiKey string
	model  string
	client *http.Client
}

// NewGeminiGenerator builds a generator; an empty model falls back to the default.
func NewGeminiGenerator(apiKey, model string) *GeminiGenerator {
	if strings.TrimSpace(model) == "" {
		model = DefaultGenerationModel
	}
	return &GeminiGenerator{apiKey: apiKey, model: model, client: &http.Client{Timeout: 120 * time.Second}}
}

type genReq struct {
	Contents []genContent `json:"contents"`
}
type genContent struct {
	Parts []genPart `json:"parts"`
}
type genPart struct {
	Text string `json:"text"`
}
type genResp struct {
	Candidates []struct {
		Content genContent `json:"content"`
	} `json:"candidates"`
}

// Generate sends one generateContent request and returns the concatenated text.
func (g *GeminiGenerator) Generate(ctx context.Context, prompt string) (string, error) {
	if strings.TrimSpace(g.apiKey) == "" {
		return "", fmt.Errorf("synth: gemini api key not configured")
	}
	bodyBytes, err := json.Marshal(genReq{Contents: []genContent{{Parts: []genPart{{Text: prompt}}}}})
	if err != nil {
		return "", fmt.Errorf("synth: marshal request: %w", err)
	}
	endpoint := fmt.Sprintf("%s/v1beta/models/%s:generateContent", geminiBaseURL, g.model)

	var respBytes []byte
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
		if err != nil {
			return "", fmt.Errorf("synth: new request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		q := req.URL.Query()
		q.Set("key", g.apiKey)
		req.URL.RawQuery = q.Encode()

		resp, err := g.client.Do(req)
		if err != nil {
			return "", fmt.Errorf("synth: gemini call: %w", err)
		}
		respBytes, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return "", fmt.Errorf("synth: read response: %w", err)
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
				return "", ctx.Err()
			case <-time.After(backoff):
				continue
			}
		}
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("synth: gemini api error %d: %s", resp.StatusCode, strings.TrimSpace(string(respBytes)))
		}
		break
	}

	var gr genResp
	if err := json.Unmarshal(respBytes, &gr); err != nil {
		return "", fmt.Errorf("synth: unmarshal response: %w", err)
	}
	var out strings.Builder
	for _, c := range gr.Candidates {
		for _, p := range c.Content.Parts {
			out.WriteString(p.Text)
		}
	}
	text := strings.TrimSpace(out.String())
	if text == "" {
		return "", fmt.Errorf("synth: gemini returned an empty completion")
	}
	return text, nil
}

// ---- claude -p generator (client-side, operator-driven) ----

// ClaudeLocalGenerator runs `claude -p` as a subprocess (prompt on stdin),
// inheriting the operator's environment/auth — the client-side executor. It is
// NEVER constructed server-side, which keeps Claude off the server.
type ClaudeLocalGenerator struct {
	BinaryPath string        // empty → "claude"
	Model      string        // empty → no --model flag (harness default)
	Timeout    time.Duration // <=0 → 120s
}

// Generate runs claude -p with the prompt on stdin and returns its stdout.
func (c ClaudeLocalGenerator) Generate(ctx context.Context, prompt string) (string, error) {
	binary := c.BinaryPath
	if binary == "" {
		binary = "claude"
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{"-p"}
	if m := strings.TrimSpace(c.Model); m != "" {
		args = append(args, "--model", m)
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("synth: claude -p exited: %s: %s", exitErr.String(), strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("synth: claude -p: %w", err)
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return "", fmt.Errorf("synth: claude -p returned no output")
	}
	return text, nil
}

// ---- objective service (server-side orchestration) ----

// ObjectiveService gathers a workspace's corpus and runs the objective task
// through its Generator. Server-side (it holds the document store); the CLI
// claude-local path orchestrates the same task over the verb surface instead.
type ObjectiveService struct {
	gen  Generator
	docs *document.Store
}

// NewObjectiveService wires the service. A nil gen disables it (Enabled()=false).
func NewObjectiveService(gen Generator, docs *document.Store) *ObjectiveService {
	return &ObjectiveService{gen: gen, docs: docs}
}

// Enabled reports whether a generator is wired.
func (s *ObjectiveService) Enabled() bool { return s != nil && s.gen != nil }

// GenerateText gathers the workspace's documents (excluding the objective doc
// itself), builds the objective prompt, and runs the generator. Returns an
// error when there is no corpus or no generator, so the caller can report a
// clear not-generated result.
func (s *ObjectiveService) GenerateText(ctx context.Context, workspaceID string) (string, error) {
	if !s.Enabled() {
		return "", fmt.Errorf("no generator configured")
	}
	res, err := s.docs.List(ctx,
		document.ListFilter{Type: "document", Scope: document.ScopeWorkspace, WorkspaceID: workspaceID},
		document.ListOptions{Limit: 200})
	if err != nil {
		return "", fmt.Errorf("synth: list corpus: %w", err)
	}
	var corpus []CorpusDoc
	for _, d := range res.Items {
		if d.Name == ObjectiveDocName {
			continue // never feed the objective into its own regeneration
		}
		got, err := s.docs.Get(ctx, document.Key{Scope: document.ScopeWorkspace, WorkspaceID: workspaceID, Name: d.Name}, document.GetOptions{})
		if err != nil || len(got.Versions) == 0 {
			continue
		}
		corpus = append(corpus, CorpusDoc{Name: d.Name, Body: got.Versions[0].Body})
	}
	if len(corpus) == 0 {
		return "", fmt.Errorf("no corpus documents to synthesize from")
	}
	return s.gen.Generate(ctx, BuildObjectivePrompt(corpus))
}
