// Package workflow parses the markdown "workflow skill" file that
// configures a story-type's lifecycle. The state machine is read
// from the file each time it's needed (lazy parse — sty_25d5b21e's
// reviewer gate is what enforces transitions; this package only
// reports what the gate is allowed to call).
//
// Skill file shape:
//
//	---
//	name: feature-workflow
//	applies_to: [feature, fix]
//	---
//
//	# Feature workflow (human-readable preamble)
//
//	```yaml
//	states: [backlog, planning, planned, in-progress, completed]
//	transitions:
//	  - {from: backlog,     to: planning,    reviewer_skill: ""}
//	  - {from: planning,    to: planned,     reviewer_skill: "plan-review"}
//	  - {from: in-progress, to: completed,   reviewer_skill: "done-review"}
//	```
//
// The frontmatter carries metadata; the fenced ```yaml block in the
// body carries the state machine. Free-form markdown around the YAML
// block is intentional — agents read the file end-to-end before
// requesting review.
//
// Parser is strict: every malformed shape returns a precise error so
// operators don't ship subtly wrong workflows. Tests cover each
// failure mode.
package workflow

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/bobmcallan/satellites/internal/frontmatter"
)

// Workflow is the parsed view of a workflow-skill file.
type Workflow struct {
	Name        string       `yaml:"-"`
	AppliesTo   []string     `yaml:"-"`
	States      []string     `yaml:"states"`
	Transitions []Transition `yaml:"transitions"`
}

// Transition is one edge in the workflow state machine.
//
// ReviewerSkill names the reviewer the request_review verb invokes
// before allowing the transition; empty string means the transition
// is unguarded (typically the initial step into a phase). Dynamic
// marks a transition that the workflow can re-enter — used by
// `dynamic-phase-insertion`-style flows in future workflows; v5
// honours the field but the request_review verb won't act on it
// until story 8 (dynamic phases) lands.
type Transition struct {
	From          string `yaml:"from"`
	To            string `yaml:"to"`
	ReviewerSkill string `yaml:"reviewer_skill"`
	Dynamic       bool   `yaml:"dynamic,omitempty"`
}

// workflowFrontmatter mirrors the subset of frontmatter fields the
// workflow file uses beyond the shared `name`. AppliesTo lives here
// because the shared frontmatter.Frontmatter is intentionally narrow
// (tags + name only).
type workflowFrontmatter struct {
	Name      string   `yaml:"name"`
	AppliesTo []string `yaml:"applies_to"`
}

// Parse reads a workflow-skill file and returns the typed state
// machine. Strict on shape — every error names exactly what is wrong.
func Parse(raw []byte) (*Workflow, error) {
	fm, body, err := frontmatter.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("workflow: frontmatter: %w", err)
	}

	// Re-parse the same frontmatter region for the workflow-only
	// fields (applies_to). frontmatter.Frontmatter doesn't carry them,
	// so we extract from the raw block ourselves.
	wfm, err := parseWorkflowFrontmatter(raw)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(wfm.Name)
	if name == "" {
		name = strings.TrimSpace(fm.Name)
	}
	if name == "" {
		return nil, fmt.Errorf("workflow: frontmatter: name required")
	}
	if len(wfm.AppliesTo) == 0 {
		return nil, fmt.Errorf("workflow: frontmatter: applies_to required (at least one story_type)")
	}
	for i, t := range wfm.AppliesTo {
		if strings.TrimSpace(t) == "" {
			return nil, fmt.Errorf("workflow: frontmatter: applies_to[%d] is empty", i)
		}
	}

	yamlBlock, err := extractYAMLBlock(body)
	if err != nil {
		return nil, err
	}

	w := Workflow{Name: name, AppliesTo: wfm.AppliesTo}
	if err := yaml.Unmarshal(yamlBlock, &w); err != nil {
		return nil, fmt.Errorf("workflow: yaml block: %w", err)
	}

	if err := w.validate(); err != nil {
		return nil, err
	}
	return &w, nil
}

// validate checks structural invariants the parser cannot catch
// during unmarshalling — empty state set, transitions that reference
// states the workflow didn't declare, duplicate edges.
func (w *Workflow) validate() error {
	if len(w.States) == 0 {
		return fmt.Errorf("workflow: yaml block: states required (at least one)")
	}
	known := make(map[string]struct{}, len(w.States))
	for i, s := range w.States {
		s = strings.TrimSpace(s)
		if s == "" {
			return fmt.Errorf("workflow: yaml block: states[%d] is empty", i)
		}
		if _, dup := known[s]; dup {
			return fmt.Errorf("workflow: yaml block: states[%d] %q is duplicated", i, s)
		}
		known[s] = struct{}{}
		w.States[i] = s
	}
	if len(w.Transitions) == 0 {
		return fmt.Errorf("workflow: yaml block: transitions required (at least one)")
	}
	seen := make(map[string]struct{}, len(w.Transitions))
	for i, t := range w.Transitions {
		from := strings.TrimSpace(t.From)
		to := strings.TrimSpace(t.To)
		if from == "" {
			return fmt.Errorf("workflow: yaml block: transitions[%d].from is empty", i)
		}
		if to == "" {
			return fmt.Errorf("workflow: yaml block: transitions[%d].to is empty", i)
		}
		if _, ok := known[from]; !ok {
			return fmt.Errorf("workflow: yaml block: transitions[%d].from %q is not a declared state", i, from)
		}
		if _, ok := known[to]; !ok {
			return fmt.Errorf("workflow: yaml block: transitions[%d].to %q is not a declared state", i, to)
		}
		key := from + "->" + to
		if _, dup := seen[key]; dup {
			return fmt.Errorf("workflow: yaml block: transitions[%d] %s is duplicated", i, key)
		}
		seen[key] = struct{}{}
		w.Transitions[i] = Transition{From: from, To: to, ReviewerSkill: strings.TrimSpace(t.ReviewerSkill), Dynamic: t.Dynamic}
	}
	return nil
}

// TransitionsFrom returns every outgoing edge from the named state.
// Empty slice if the state is unknown — callers gate on
// `len(.)==0` to detect a terminal state.
func (w *Workflow) TransitionsFrom(state string) []Transition {
	out := make([]Transition, 0, 2)
	for _, t := range w.Transitions {
		if t.From == state {
			out = append(out, t)
		}
	}
	return out
}

// PickTransition resolves the single transition a reviewer gate should
// run from currentStatus, and the gate skill that guards it:
//
//   - Any outgoing edge marked dynamic → the workflow itself is the gate
//     (gateSkill = workflow name, isDynamic = true).
//   - Otherwise the single outgoing edge with a non-empty reviewer_skill
//     is the gated edge. Zero gated edges (every outgoing edge is an
//     ungated self-transition) or more than one are errors — the caller
//     uses the body-only patch path or the workflow must mark the choice
//     dynamic, respectively.
//
// This is the one authoritative transition-resolution rule; both the
// server verb and the client-side reviewer command call it.
func (w *Workflow) PickTransition(currentStatus string) (Transition, string, bool, error) {
	outgoing := w.TransitionsFrom(currentStatus)
	if len(outgoing) == 0 {
		return Transition{}, "", false, fmt.Errorf("no outgoing transitions from status %q", currentStatus)
	}
	for _, t := range outgoing {
		if t.Dynamic {
			return t, w.Name, true, nil
		}
	}
	var gated []Transition
	for _, t := range outgoing {
		if strings.TrimSpace(t.ReviewerSkill) != "" {
			gated = append(gated, t)
		}
	}
	if len(gated) == 0 {
		return Transition{}, "", false, fmt.Errorf("no gated transition from status %q (every outgoing edge has empty reviewer_skill — use the body-only patch path)", currentStatus)
	}
	if len(gated) > 1 {
		return Transition{}, "", false, fmt.Errorf("multiple gated transitions from status %q — workflow must mark the choice as dynamic", currentStatus)
	}
	return gated[0], gated[0].ReviewerSkill, false, nil
}

// FindTransition returns the edge from→to or false if undeclared.
// request_review uses this to decide whether the requested transition
// is even legal before invoking the reviewer skill.
func (w *Workflow) FindTransition(from, to string) (Transition, bool) {
	for _, t := range w.Transitions {
		if t.From == from && t.To == to {
			return t, true
		}
	}
	return Transition{}, false
}

// parseWorkflowFrontmatter re-decodes the YAML between the file's two
// `---` delimiters so we can read `applies_to` (which the shared
// frontmatter struct doesn't carry). Returns a zero value if the file
// has no frontmatter at all — caller's required-field check then
// reports the missing fields.
func parseWorkflowFrontmatter(raw []byte) (workflowFrontmatter, error) {
	delim := []byte("---")
	if !bytes.HasPrefix(raw, append(delim, '\n')) && !bytes.HasPrefix(raw, append(delim, '\r', '\n')) {
		return workflowFrontmatter{}, nil
	}
	rest := raw[len(delim):]
	if len(rest) > 0 && rest[0] == '\r' {
		rest = rest[1:]
	}
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	}
	closeIdx := bytes.Index(rest, append([]byte("\n"), delim...))
	if closeIdx < 0 {
		return workflowFrontmatter{}, fmt.Errorf("workflow: frontmatter: opening --- without closing ---")
	}
	block := rest[:closeIdx]
	var wfm workflowFrontmatter
	if err := yaml.Unmarshal(block, &wfm); err != nil {
		return workflowFrontmatter{}, fmt.Errorf("workflow: frontmatter: %w", err)
	}
	return wfm, nil
}

// ExtractYAMLBlock pulls the first ```yaml fenced block out of a
// markdown body. Exposed so other readers of fenced-YAML documents
// (e.g. the project-config loader in internal/verb) share the exact
// fence-extraction rule instead of re-implementing it — fixing the
// fence-vs-raw parse bug once, in one place. Returns an error when no
// line-anchored ```yaml fence is present.
func ExtractYAMLBlock(body []byte) ([]byte, error) {
	return extractYAMLBlock(body)
}

// extractYAMLBlock pulls the first ```yaml fenced block out of the
// body. The opening fence must start a line — inline mentions of
// ```yaml in prose don't trigger extraction. Anything outside the
// block is human-readable preamble and is ignored by the parser.
func extractYAMLBlock(body []byte) ([]byte, error) {
	const open = "```yaml"
	const fence = "```"
	// Match `\n```yaml` (or the body opening with `` ```yaml ``) so
	// only line-anchored fences are recognised.
	idx := -1
	if bytes.HasPrefix(body, []byte(open)) {
		idx = 0
	} else if at := bytes.Index(body, []byte("\n"+open)); at >= 0 {
		idx = at + 1
	}
	if idx < 0 {
		return nil, fmt.Errorf("workflow: body: no ```yaml block found (the state machine must live in a ```yaml fenced block)")
	}
	rest := body[idx+len(open):]
	if len(rest) > 0 && rest[0] == '\r' {
		rest = rest[1:]
	}
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	}
	closeIdx := bytes.Index(rest, []byte("\n"+fence))
	if closeIdx < 0 {
		return nil, fmt.Errorf("workflow: body: opening ```yaml without closing ```")
	}
	return rest[:closeIdx], nil
}
