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

// objectiveSpec is the objective TASK's instruction — the one spec the objective
// generation runs, and now the sole caller of GenerateOverCorpus (the legacy
// workspace_task_run caller was retired with epic:project-tasks).
const objectiveSpec = "You are assisting a delivery/project manager. Below are the documents collected for a client engagement (a workspace corpus). " +
	"Synthesize a concise engagement OBJECTIVE: 2–4 short paragraphs stating what this engagement is about, its goal, and the current focus — grounded only in the documents. " +
	"Do not invent facts not supported by the documents. Output the objective as plain markdown, no preamble."

// BuildTaskPrompt renders a task spec + the workspace corpus into the prompt the
// generator runs. The spec is the task's instruction (an objective spec, or a
// kind:task skill body); the corpus is enveloped the same way for every task.
// Shared data, not executor-specific logic.
func BuildTaskPrompt(spec string, corpus []CorpusDoc) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(spec))
	b.WriteString("\n\n--- DOCUMENTS ---\n")
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

// BuildObjectivePrompt is the objective TASK spec rendered over the corpus —
// BuildTaskPrompt with the objective instruction. Preserved for callers that
// build the objective prompt directly (client-side claude -p executor).
func BuildObjectivePrompt(corpus []CorpusDoc) string {
	return BuildTaskPrompt(objectiveSpec, corpus)
}

// ---- Gemini generator (server-side, autonomous) ----

// DefaultGenerationModel is a generateContent-capable Gemini model.
const (
	DefaultGenerationModel = "gemini-2.5-flash"
	geminiBaseURL          = "https://generativelanguage.googleapis.com"
)

// GeminiGenerator calls Gemini's generateContent. Server-side: the autonomous
// executor used when no operator/client is driving. Credentials resolve per
// Generate via provide, so the api key and model are read at request time
// (substrate key-values, env fallback).
type GeminiGenerator struct {
	provide func(context.Context) (apiKey, model string)
	client  *http.Client
}

// NewGeminiGenerator builds a generator from fixed credentials; an empty model
// falls back to the default. Kept for callers with static config.
func NewGeminiGenerator(apiKey, model string) *GeminiGenerator {
	if strings.TrimSpace(model) == "" {
		model = DefaultGenerationModel
	}
	return NewGeminiGeneratorFunc(func(context.Context) (string, string) { return apiKey, model })
}

// NewGeminiGeneratorFunc builds a generator from a per-request credential
// provider (api key + model resolved at Generate time).
func NewGeminiGeneratorFunc(provide func(context.Context) (apiKey, model string)) *GeminiGenerator {
	return &GeminiGenerator{provide: provide, client: &http.Client{Timeout: 120 * time.Second}}
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
	apiKey, model := g.provide(ctx)
	if strings.TrimSpace(model) == "" {
		model = DefaultGenerationModel
	}
	if strings.TrimSpace(apiKey) == "" {
		return "", fmt.Errorf("synth: gemini api key not configured")
	}
	bodyBytes, err := json.Marshal(genReq{Contents: []genContent{{Parts: []genPart{{Text: prompt}}}}})
	if err != nil {
		return "", fmt.Errorf("synth: marshal request: %w", err)
	}
	endpoint := fmt.Sprintf("%s/v1beta/models/%s:generateContent", geminiBaseURL, model)

	var respBytes []byte
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
		if err != nil {
			return "", fmt.Errorf("synth: new request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		q := req.URL.Query()
		q.Set("key", apiKey)
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
	return s.GenerateOverCorpus(ctx, workspaceID, objectiveSpec, ObjectiveDocName)
}

// GenerateOverCorpus runs an arbitrary task spec over a workspace's corpus: it
// gathers the workspace documents (excluding excludeName — the task's own output,
// so a run never feeds its previous result into itself), builds the task prompt,
// and runs the generator. The objective generation is the sole caller (spec =
// objectiveSpec, excludeName = ObjectiveDocName).
func (s *ObjectiveService) GenerateOverCorpus(ctx context.Context, workspaceID, spec, excludeName string) (string, error) {
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
		if excludeName != "" && d.Name == excludeName {
			continue // never feed the task's own output back into the run
		}
		got, err := s.docs.Get(ctx, document.Key{Scope: document.ScopeWorkspace, WorkspaceID: workspaceID, Name: d.Name}, document.GetOptions{})
		if err != nil || len(got.Versions) == 0 {
			continue
		}
		corpus = append(corpus, CorpusDoc{Name: d.Name, Body: got.Versions[0].Body})
	}
	return s.GenerateFromCorpus(ctx, spec, corpus)
}

// GenerateFromCorpus builds the task prompt from an already-gathered corpus and
// runs the generator. Split from GenerateOverCorpus so the generation half is
// unit-testable with a fake Generator, no document store required.
func (s *ObjectiveService) GenerateFromCorpus(ctx context.Context, spec string, corpus []CorpusDoc) (string, error) {
	if !s.Enabled() {
		return "", fmt.Errorf("no generator configured")
	}
	if len(corpus) == 0 {
		return "", fmt.Errorf("no corpus documents to synthesize from")
	}
	return s.gen.Generate(ctx, BuildTaskPrompt(spec, corpus))
}
