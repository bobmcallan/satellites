// `satellites evidence review <story>` (sty_5fa71635, epic:measure-mode M5).
// The session-review SYNTHESIS layer: it assembles the signal the loop already
// captured — processtrace.Reconcile (declared × actual), processtrace.Audit
// (loop anomalies), and the workstate evidence store (gate runs + CI outcomes) —
// into a per-story review.md + metrics.json under .satellites/work/<story>/, plus
// an improvement-suggestions section synthesised by a best-effort `claude -p`
// pass (the workflow-design subagent pattern) that degrades to a deterministic
// anomaly/rejection-derived fallback when claude is unavailable.
//
// It adds NO new capture (M4 was cancelled) — it is a read + render over existing
// signal. Speed is recorded, never scored (the epic's rule).

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/processtrace"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workflow"
	"github.com/bobmcallan/satellites/internal/workstate"
)

const evidenceReviewTimeout = 3 * time.Minute

// evidenceReviewSystemPrompt drives the claude -p suggestions pass. The metrics
// JSON arrives on stdin; the agent returns plain markdown bullets only.
const evidenceReviewSystemPrompt = `You are a process-review synthesiser for the satellites reviewer loop.
Input on stdin is a JSON metrics object for ONE story's run through its
reviewer-gated workflow: the gates run, rejections with reasons, detected loop
anomalies, per-transition step summaries, and the outcome. Output 2-5 concrete,
actionable improvement suggestions aimed at the workflow, the gate/reviewer
skills, or the executor process — plain markdown bullets, no preamble, no JSON,
no headings. Ground each suggestion in a specific anomaly, rejection reason, or
gap in the metrics. If the run was clean (no anomalies, no rejections), say so
in a single bullet rather than inventing problems.`

// ciOutcome is one recorded CI stage result projected into the metrics.
type ciOutcome struct {
	Stage  string `json:"stage"`
	Result string `json:"result"`
	Ref    string `json:"ref,omitempty"`
}

// processMetrics is the process-adherence slice of the review: what the gates did.
type processMetrics struct {
	GatesRun         int                            `json:"gates_run"`
	Accepted         int                            `json:"accepted"`
	Rejections       int                            `json:"rejections"`
	RejectionReasons []string                       `json:"rejection_reasons,omitempty"`
	Transitions      []processtrace.TransitionTrace `json:"transitions"`
}

// outcomeMetrics is the result slice: did it land, and what CI said.
type outcomeMetrics struct {
	ReachedTerminal bool        `json:"reached_terminal"`
	TerminalStatus  string      `json:"terminal_status"`
	CI              []ciOutcome `json:"ci,omitempty"`
}

// speedMetrics is RECORDED, never scored (the epic's rule) — slow-with-quality
// beats fast-without, so these are reported as facts, not graded.
type speedMetrics struct {
	FirstActivity string `json:"first_activity,omitempty"`
	LastActivity  string `json:"last_activity,omitempty"`
	WallClock     string `json:"wall_clock,omitempty"`
}

// reviewMetrics is the metrics.json shape — the machine-readable review.
type reviewMetrics struct {
	Story       string                 `json:"story"`
	Type        string                 `json:"type"`
	Status      string                 `json:"status"`
	Workflow    string                 `json:"workflow,omitempty"`
	GeneratedAt string                 `json:"generated_at"`
	Process     processMetrics         `json:"process"`
	Outcome     outcomeMetrics         `json:"outcome"`
	Speed       speedMetrics           `json:"speed"`
	Anomalies   []processtrace.Finding `json:"anomalies"`
}

// buildReviewMetrics is the pure assembly core — it folds the reconciled trace,
// the audit findings, and the evidence rows into the metrics shape. No I/O, so
// it is unit-tested directly.
func buildReviewMetrics(trace processtrace.ProcessTrace, findings []processtrace.Finding, evidence []workstate.Evidence, generatedAt time.Time) reviewMetrics {
	m := reviewMetrics{
		Story:       trace.StoryID,
		Type:        trace.StoryType,
		Status:      trace.CurrentStatus,
		Workflow:    trace.WorkflowName,
		GeneratedAt: generatedAt.Format(time.RFC3339),
		Anomalies:   findings,
	}
	if m.Anomalies == nil {
		m.Anomalies = []processtrace.Finding{}
	}

	for _, t := range trace.Transitions {
		m.Process.Transitions = append(m.Process.Transitions, t)
		switch t.Status {
		case processtrace.StatusAccepted, processtrace.StatusFired:
			m.Process.GatesRun++
			m.Process.Accepted++
		case processtrace.StatusRejected:
			m.Process.GatesRun++
		}
		if t.RejectCount > 0 {
			m.Process.Rejections += t.RejectCount
			if t.Status == processtrace.StatusRejected && strings.TrimSpace(t.Verdict) != "" {
				m.Process.RejectionReasons = append(m.Process.RejectionReasons,
					fmt.Sprintf("%s→%s: %s", t.From, t.To, firstLine(t.Verdict)))
			}
		}
	}

	m.Outcome.TerminalStatus = trace.CurrentStatus
	m.Outcome.ReachedTerminal = trace.CurrentStatus == "done" || trace.CurrentStatus == "cancelled"
	for _, e := range evidence {
		if e.Kind == workstate.EvidenceCI {
			m.Outcome.CI = append(m.Outcome.CI, ciOutcome{Stage: e.Label, Result: e.Decision, Ref: e.Ref})
		}
	}

	var first, last time.Time
	for _, t := range trace.Transitions {
		if t.At == nil {
			continue
		}
		if first.IsZero() || t.At.Before(first) {
			first = *t.At
		}
		if t.At.After(last) {
			last = *t.At
		}
	}
	if !first.IsZero() {
		m.Speed.FirstActivity = first.Format(time.RFC3339)
		m.Speed.LastActivity = last.Format(time.RFC3339)
		m.Speed.WallClock = last.Sub(first).Round(time.Second).String()
	}
	return m
}

// fallbackSuggestions derives improvement bullets deterministically from the
// metrics when the claude -p pass is unavailable or empty (AC3's degrade path).
func fallbackSuggestions(m reviewMetrics) string {
	if len(m.Anomalies) == 0 && m.Process.Rejections == 0 {
		return "- No process anomalies or gate rejections recorded — the loop ran clean. No changes suggested.\n"
	}
	var b strings.Builder
	for _, f := range m.Anomalies {
		fmt.Fprintf(&b, "- [%s] %s — %s\n", f.Severity, f.Anomaly, f.Detail)
	}
	for _, r := range m.Process.RejectionReasons {
		fmt.Fprintf(&b, "- gate rejection to learn from: %s\n", r)
	}
	return b.String()
}

// renderReviewMarkdown renders the human-readable review.md. Pure — unit-tested.
func renderReviewMarkdown(m reviewMetrics, suggestions string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Session review — %s\n\n", m.Story)
	fmt.Fprintf(&b, "- **Type:** %s\n- **Status:** %s\n- **Workflow:** %s\n- **Generated:** %s\n\n",
		dash(m.Type), dash(m.Status), dash(m.Workflow), m.GeneratedAt)

	b.WriteString("## Process adherence\n\n")
	fmt.Fprintf(&b, "Gates run: %d · accepted: %d · rejections: %d\n\n",
		m.Process.GatesRun, m.Process.Accepted, m.Process.Rejections)
	b.WriteString("| from | to | gate | status | rejects | step summary |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	for _, t := range m.Process.Transitions {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %d | %s |\n",
			t.From, t.To, dash(t.ReviewerSkill), t.Status, t.RejectCount, oneLine(t.StepSummary))
	}
	if len(m.Process.RejectionReasons) > 0 {
		b.WriteString("\n**Rejection reasons:**\n")
		for _, r := range m.Process.RejectionReasons {
			fmt.Fprintf(&b, "- %s\n", r)
		}
	}

	fmt.Fprintf(&b, "\n## Outcome\n\nReached terminal: %v (%s)\n", m.Outcome.ReachedTerminal, dash(m.Outcome.TerminalStatus))
	if len(m.Outcome.CI) > 0 {
		b.WriteString("\nCI outcomes:\n")
		for _, c := range m.Outcome.CI {
			fmt.Fprintf(&b, "- %s: %s %s\n", c.Stage, c.Result, c.Ref)
		}
	}

	b.WriteString("\n## Anomalies\n\n")
	if len(m.Anomalies) == 0 {
		b.WriteString("None.\n")
	} else {
		for _, f := range m.Anomalies {
			fmt.Fprintf(&b, "- [%s] %s — %s\n", f.Severity, f.Anomaly, f.Detail)
		}
	}

	fmt.Fprintf(&b, "\n## Speed (recorded, not scored)\n\nFirst: %s · Last: %s · Wall-clock: %s\n",
		dash(m.Speed.FirstActivity), dash(m.Speed.LastActivity), dash(m.Speed.WallClock))

	b.WriteString("\n## Improvement suggestions\n\n")
	b.WriteString(strings.TrimRight(suggestions, "\n"))
	b.WriteString("\n")
	return b.String()
}

type evidenceReviewOpts struct {
	Story   string
	StateDB string
	WorkDir string
	JSON    bool
	// Compare is an optional path to a previous metrics.json; when set, the run
	// appends a "## Delta vs previous" section and writes delta.json (M6).
	Compare string
}

// suggestFn produces improvement suggestions from the metrics JSON. Injected so
// the core is testable without invoking claude.
type suggestFn func(ctx context.Context, metricsJSON string) string

// runEvidenceReview assembles a story's review from existing signal and writes
// review.md + metrics.json. dispatch + suggest are injected for testability.
func runEvidenceReview(ctx context.Context, out io.Writer, opts evidenceReviewOpts, dispatch verbDispatch, suggest suggestFn, now time.Time) error {
	if strings.TrimSpace(opts.Story) == "" {
		return fmt.Errorf("story id required")
	}

	// 1. Resolve the story (status / type / body for the workflow).
	getReq, err := json.Marshal(verb.DocumentGetRequest{ID: opts.Story})
	if err != nil {
		return err
	}
	getRaw, err := dispatch(ctx, "document_get", getReq)
	if err != nil {
		return fmt.Errorf("evidence review: get story: %w", err)
	}
	var resp verb.DocumentGetResponse
	if err := json.Unmarshal(getRaw, &resp); err != nil {
		return fmt.Errorf("evidence review: decode story: %w", err)
	}
	body := resp.RawBody
	if body == "" && len(resp.Versions) > 0 {
		body = resp.Versions[0].Body
	}

	// 2. Ledger trail (reused projection) + 3. declared workflow.
	entries, err := auditLedger(ctx, dispatch, opts.Story)
	if err != nil {
		return fmt.Errorf("evidence review: %w", err)
	}
	wf, _ := workflow.ParseBody([]byte(body)) // nil-tolerant: no ## Workflow ⇒ empty trace

	// 4. Reconcile (declared × actual) + 5. audit (anomalies).
	trace := processtrace.Reconcile(opts.Story, resp.Document.Type, resp.Document.Status, wf, entries)
	findings := processtrace.Audit(processtrace.StoryAudit{
		StoryID:       opts.Story,
		StoryType:     resp.Document.Type,
		CurrentStatus: resp.Document.Status,
		Entries:       entries,
	})

	// 6. Evidence store (gate runs + CI) — best-effort: an absent store yields none.
	var evidence []workstate.Evidence
	if store, oerr := workstate.Open(opts.StateDB); oerr == nil {
		evidence, _ = store.ListEvidence(opts.Story)
		store.Close()
	}

	// 7. Metrics + 8. suggestions (claude -p, degrading to deterministic).
	m := buildReviewMetrics(trace, findings, evidence, now)
	metricsJSON, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("evidence review: marshal metrics: %w", err)
	}
	suggestions := ""
	if suggest != nil {
		suggestions = suggest(ctx, string(metricsJSON))
	}
	if strings.TrimSpace(suggestions) == "" {
		suggestions = fallbackSuggestions(m)
	}
	md := renderReviewMarkdown(m, suggestions)

	dir := filepath.Join(opts.WorkDir, opts.Story)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("evidence review: create %s: %w", dir, err)
	}

	// 8b. Optional delta against a previous metrics.json (M6, sty_37fef613).
	var deltaPath string
	if strings.TrimSpace(opts.Compare) != "" {
		prevBytes, rerr := os.ReadFile(opts.Compare)
		if rerr != nil {
			return fmt.Errorf("evidence review: read --compare %s: %w", opts.Compare, rerr)
		}
		var prev reviewMetrics
		if jerr := json.Unmarshal(prevBytes, &prev); jerr != nil {
			return fmt.Errorf("evidence review: parse --compare %s: %w", opts.Compare, jerr)
		}
		delta := compareReviewMetrics(prev, m)
		md += renderDeltaMarkdown(delta)
		deltaJSON, derr := json.MarshalIndent(delta, "", "  ")
		if derr != nil {
			return fmt.Errorf("evidence review: marshal delta: %w", derr)
		}
		deltaPath = filepath.Join(dir, "delta.json")
		if werr := os.WriteFile(deltaPath, append(deltaJSON, '\n'), 0o644); werr != nil {
			return fmt.Errorf("evidence review: write %s: %w", deltaPath, werr)
		}
	}

	// 9. Write artifacts under .satellites/work/<story>/.
	mPath := filepath.Join(dir, "metrics.json")
	rPath := filepath.Join(dir, "review.md")
	if err := os.WriteFile(mPath, append(metricsJSON, '\n'), 0o644); err != nil {
		return fmt.Errorf("evidence review: write %s: %w", mPath, err)
	}
	if err := os.WriteFile(rPath, []byte(md), 0o644); err != nil {
		return fmt.Errorf("evidence review: write %s: %w", rPath, err)
	}

	fmt.Fprintf(out, "wrote %s\n", rPath)
	fmt.Fprintf(out, "wrote %s\n", mPath)
	if deltaPath != "" {
		fmt.Fprintf(out, "wrote %s\n", deltaPath)
	}
	if opts.JSON {
		out.Write(append(metricsJSON, '\n'))
	}
	return nil
}

// claudeSuggestImprovements runs the best-effort claude -p synthesis pass over
// the metrics JSON. Any failure (no claude, timeout, error) returns "" so the
// caller falls back to deterministic suggestions.
func claudeSuggestImprovements(ctx context.Context, claudeBin, metricsJSON string) string {
	bin := strings.TrimSpace(claudeBin)
	if bin == "" {
		bin = strings.TrimSpace(os.Getenv("SATELLITES_CLAUDE_BIN"))
	}
	if bin == "" {
		bin = "claude"
	}
	cctx, cancel := context.WithTimeout(ctx, evidenceReviewTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, "-p", "--allowedTools", "Read", "--append-system-prompt", evidenceReviewSystemPrompt)
	cmd.Stdin = strings.NewReader(metricsJSON)
	cmd.Env = os.Environ()
	outBytes, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(outBytes))
}

// firstLine returns the first non-empty line of s, trimmed — used to keep a
// rejection reason to one line in the metrics.
func firstLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			return t
		}
	}
	return ""
}

// oneLine collapses s to a single space-joined line for a markdown table cell.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// dash renders an empty string as an em-dash placeholder.
func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}
