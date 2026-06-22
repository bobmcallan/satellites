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
	States      []State      `yaml:"states"`
	Transitions []Transition `yaml:"transitions"`
}

// State is one declared workflow state. Authored either as a bare scalar
// (`- backlog`) or an object (`- {name: techdebt-review, actor: satellites}`).
// Actor says WHO acts while a story sits in the state — an open vocabulary
// (any team shape can be expressed); the names executor / reviewer /
// satellites / operator are the reserved vocabulary the dispatcher attaches
// semantics to. Empty actor = unspecified = pre-actor semantics.
//
// Command is the deterministic command an `actor: satellites` state runs to
// advance itself — exit 0 selects the pass edge, non-zero the fail edge. It is
// legal only on satellites states; a satellites state may omit it (the
// dispatcher then refuses to advance rather than guessing).
type State struct {
	Name    string `yaml:"name"`
	Actor   string `yaml:"actor,omitempty"`
	Command string `yaml:"command,omitempty"`
}

// UnmarshalYAML accepts both the scalar and the mapping form.
func (s *State) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		s.Name = value.Value
		return nil
	case yaml.MappingNode:
		type plain State
		var p plain
		if err := value.Decode(&p); err != nil {
			return err
		}
		*s = State(p)
		return nil
	default:
		return fmt.Errorf("state must be a string or a {name, actor} mapping")
	}
}

// MarshalYAML keeps round-trips stable: a state with no actor re-emits as the
// bare scalar it was authored as.
func (s State) MarshalYAML() (interface{}, error) {
	if s.Actor == "" {
		return s.Name, nil
	}
	type plain State
	return plain(s), nil
}

// Transition is one edge in the workflow state machine.
//
// ReviewerSkill names the reviewer the request_review verb invokes
// before allowing the transition; empty string means the transition
// is unguarded (typically the initial step into a phase). WorkSkill
// names the "do" half of the step — the work skill the executor runs on
// this edge before requesting the reviewer (e.g. `commit-push`). It is
// optional and display/route only: it names a skill to run, it never
// enacts status (the reviewer remains the sole status-enactor). A
// transition with both work_skill and reviewer_skill IS a step. Dynamic
// marks a transition that the workflow can re-enter — used by
// `dynamic-phase-insertion`-style flows in future workflows; v5
// honours the field but the request_review verb won't act on it
// until story 8 (dynamic phases) lands.
//
// On selects the edge by the reviewer's decision — "pass" for accept,
// "fail" for reject (a reject is then a real transition, not a stay).
// MaxIterations bounds a fail loop; OnExhausted names the declared state
// the Nth failure lands in (the two require each other). Trigger
// "checkpoint" marks an edge entered by the executor's checkpoint rather
// than a review decision. An edge with none of these keeps today's
// accept-moves/reject-stays semantics.
type Transition struct {
	From          string `yaml:"from"`
	To            string `yaml:"to"`
	ReviewerSkill string `yaml:"reviewer_skill"`
	WorkSkill     string `yaml:"work_skill,omitempty"`
	Dynamic       bool   `yaml:"dynamic,omitempty"`
	On            string `yaml:"on,omitempty"`
	MaxIterations int    `yaml:"max_iterations,omitempty"`
	OnExhausted   string `yaml:"on_exhausted,omitempty"`
	Trigger       string `yaml:"trigger,omitempty"`
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
	// Materialised SKILL.md files carry a leading client-injected
	// sync-identity comment; strip it once so every reader below
	// (frontmatter.Parse, parseWorkflowFrontmatter, extractYAMLBlock)
	// sees the authored content.
	raw = frontmatter.StripSyncStamp(raw)
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
// states the workflow didn't declare, duplicate edges, and the v2
// loop rules (pass/fail pairing, bounded fail-cycles, exhaustion
// targets). Legacy workflows (no on/actor/bounds) hit none of the v2
// paths and validate exactly as before.
func (w *Workflow) validate() error {
	if len(w.States) == 0 {
		return fmt.Errorf("workflow: yaml block: states required (at least one)")
	}
	known := make(map[string]struct{}, len(w.States))
	for i, s := range w.States {
		name := strings.TrimSpace(s.Name)
		if name == "" {
			return fmt.Errorf("workflow: yaml block: states[%d] is empty", i)
		}
		if _, dup := known[name]; dup {
			return fmt.Errorf("workflow: yaml block: states[%d] %q is duplicated", i, name)
		}
		known[name] = struct{}{}
		actor := strings.TrimSpace(s.Actor)
		if s.Actor != "" && actor == "" {
			return fmt.Errorf("workflow: yaml block: states[%d] %q has a blank actor — omit the key or name who acts", i, name)
		}
		command := strings.TrimSpace(s.Command)
		if command != "" && actor != "satellites" {
			return fmt.Errorf("workflow: yaml block: states[%d] %q carries a command but its actor is not satellites — only a satellites state runs a deterministic command", i, name)
		}
		w.States[i] = State{Name: name, Actor: actor, Command: command}
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
		on := strings.TrimSpace(t.On)
		if on != "" && on != "pass" && on != "fail" {
			return fmt.Errorf("workflow: yaml block: transitions[%d].on %q is not pass or fail", i, t.On)
		}
		trigger := strings.TrimSpace(t.Trigger)
		if trigger != "" && trigger != "checkpoint" {
			return fmt.Errorf("workflow: yaml block: transitions[%d].trigger %q is not checkpoint", i, t.Trigger)
		}
		onExhausted := strings.TrimSpace(t.OnExhausted)
		if t.MaxIterations < 0 {
			return fmt.Errorf("workflow: yaml block: transitions[%d].max_iterations %d is negative", i, t.MaxIterations)
		}
		if t.MaxIterations > 0 && on != "fail" {
			return fmt.Errorf("workflow: yaml block: transitions[%d] carries max_iterations but is not an `on: fail` edge — a bound is only meaningful on a fail loop", i)
		}
		if t.MaxIterations > 0 && onExhausted == "" {
			return fmt.Errorf("workflow: yaml block: transitions[%d] carries max_iterations %d with no on_exhausted — the Nth failure has nowhere to land", i, t.MaxIterations)
		}
		if onExhausted != "" {
			if t.MaxIterations == 0 {
				return fmt.Errorf("workflow: yaml block: transitions[%d] carries on_exhausted %q with no max_iterations — an exhaustion target needs a bound", i, onExhausted)
			}
			if _, ok := known[onExhausted]; !ok {
				return fmt.Errorf("workflow: yaml block: transitions[%d].on_exhausted %q is not a declared state", i, onExhausted)
			}
		}
		key := from + "->" + to + "/" + on
		if _, dup := seen[key]; dup {
			return fmt.Errorf("workflow: yaml block: transitions[%d] %s->%s is duplicated", i, from, to)
		}
		seen[key] = struct{}{}
		w.Transitions[i] = Transition{
			From: from, To: to,
			ReviewerSkill: strings.TrimSpace(t.ReviewerSkill),
			WorkSkill:     strings.TrimSpace(t.WorkSkill),
			Dynamic:       t.Dynamic,
			On:            on,
			MaxIterations: t.MaxIterations,
			OnExhausted:   onExhausted,
			Trigger:       trigger,
		}
	}
	if err := w.validateLoops(); err != nil {
		return err
	}
	return nil
}

// validateLoops enforces the v2 loop rules across the whole edge set:
// pass/fail edges come in pairs from a reviewed state, and every fail
// edge that closes a cycle carries a bound (no infinite process).
func (w *Workflow) validateLoops() error {
	// Pass/fail pairing per source state.
	hasPass := map[string]bool{}
	hasFail := map[string]bool{}
	for _, t := range w.Transitions {
		switch t.On {
		case "pass":
			hasPass[t.From] = true
		case "fail":
			hasFail[t.From] = true
		}
	}
	for from := range hasPass {
		if !hasFail[from] {
			return fmt.Errorf("workflow: yaml block: state %q has an `on: pass` edge but no `on: fail` edge — a reviewed state needs both outcomes", from)
		}
	}
	for from := range hasFail {
		if !hasPass[from] {
			return fmt.Errorf("workflow: yaml block: state %q has an `on: fail` edge but no `on: pass` edge — a reviewed state needs both outcomes", from)
		}
	}
	// Bounded loops: an unbounded fail edge must not close a cycle.
	for _, t := range w.Transitions {
		if t.On != "fail" || t.MaxIterations > 0 {
			continue
		}
		if w.reaches(t.To, t.From) {
			return fmt.Errorf("workflow: yaml block: `on: fail` edge %s->%s closes a cycle with no max_iterations — loops must be bounded", t.From, t.To)
		}
	}
	return nil
}

// reaches reports whether `to` is reachable from `from` over the
// transition digraph (DFS; both endpoints are declared states).
func (w *Workflow) reaches(from, to string) bool {
	if from == to {
		return true
	}
	visited := map[string]bool{}
	stack := []string{from}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if visited[cur] {
			continue
		}
		visited[cur] = true
		for _, t := range w.Transitions {
			if t.From != cur {
				continue
			}
			if t.To == to {
				return true
			}
			if !visited[t.To] {
				stack = append(stack, t.To)
			}
		}
	}
	return false
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

// InitialState returns the workflow's entry state: the declared state with no
// incoming transition. Falls back to the first declared state when every state
// has an incoming edge (an unusual cyclic workflow). Used to classify the
// pre-work state that should NOT authorise edits.
func (w *Workflow) InitialState() string {
	hasIncoming := make(map[string]bool, len(w.Transitions))
	for _, t := range w.Transitions {
		hasIncoming[t.To] = true
		// An exhaustion target is entered by the client's enactment — a
		// real incoming transition; an escalation state is never the entry.
		if t.OnExhausted != "" {
			hasIncoming[t.OnExhausted] = true
		}
	}
	for _, s := range w.States {
		if !hasIncoming[s.Name] {
			return s.Name
		}
	}
	if len(w.States) > 0 {
		return w.States[0].Name
	}
	return ""
}

// ActorOf returns the actor declared on the named state — empty when the
// state is unknown or declares no actor (pre-actor semantics).
func (w *Workflow) ActorOf(state string) string {
	s, _ := w.StateOf(state)
	return s.Actor
}

// StateOf returns the full declared state by name; ok is false when the
// state is not declared.
func (w *Workflow) StateOf(state string) (State, bool) {
	for _, s := range w.States {
		if s.Name == state {
			return s, true
		}
	}
	return State{}, false
}

// IsTerminal reports whether a declared state has no outgoing transition (a
// finished state). An undeclared state is treated as terminal (no edges).
func (w *Workflow) IsTerminal(state string) bool {
	return len(w.TransitionsFrom(state)) == 0
}

// TerminalStates returns every declared state with no outgoing transition.
func (w *Workflow) TerminalStates() []string {
	var out []string
	for _, s := range w.States {
		if w.IsTerminal(s.Name) {
			out = append(out, s.Name)
		}
	}
	return out
}

// IsEditable reports whether file edits should be permitted while a story sits
// in `state`: a working state PAST the entry gate and BEFORE the close gate —
// i.e. neither the initial (pre-entry) state nor a terminal (finished) state.
// Derived purely from the workflow shape; no hard-coded state names, so a
// re-authored workflow needs no code change.
func (w *Workflow) IsEditable(state string) bool {
	s := strings.TrimSpace(state)
	if s == "" || !w.hasState(s) {
		return false
	}
	return s != w.InitialState() && !w.IsTerminal(s)
}

// IsCommitStep reports whether `state` is the workflow's commit-push step: a
// state with an outgoing transition whose work_skill is `commit-push`. The
// harness binds `git commit`/`git push` to this step so one story ↔ one commit ↔
// one release holds — committing is authorised only AT the ship step, not at any
// editable phase. Derived purely from the workflow shape (the work_skill field),
// so a re-authored workflow needs no code change. An unknown state is false.
func (w *Workflow) IsCommitStep(state string) bool {
	for _, t := range w.TransitionsFrom(strings.TrimSpace(state)) {
		if strings.TrimSpace(t.WorkSkill) == "commit-push" {
			return true
		}
	}
	return false
}

// EditableStates returns every state for which IsEditable is true.
func (w *Workflow) EditableStates() []string {
	var out []string
	for _, s := range w.States {
		if w.IsEditable(s.Name) {
			out = append(out, s.Name)
		}
	}
	return out
}

// HasState reports whether s is a declared state of this workflow. Callers use
// it to distinguish "unknown status" (don't classify) from "non-editable".
func (w *Workflow) HasState(s string) bool { return w.hasState(s) }

// Equivalent reports whether two workflows define the same state machine —
// the same set of states (name + actor + command) and the same set of
// transitions (every field). Order-independent; Name and AppliesTo are
// ignored (they are identity/selection, not the machine). Used to reconcile a
// story's embedded `## Workflow` against the resolved governing workflow: a
// false result is drift — the embedded copy has been changed away from the
// authoritative definition (e.g. a weakened gate), which must be surfaced, not
// honoured (sty_0889de7a).
func (w *Workflow) Equivalent(other *Workflow) bool {
	if w == nil || other == nil {
		return w == other
	}
	stateKey := func(s State) string { return s.Name + "\x00" + s.Actor + "\x00" + s.Command }
	transKey := func(t Transition) string {
		return strings.Join([]string{t.From, t.To, t.ReviewerSkill, t.On, t.Trigger, t.OnExhausted,
			fmt.Sprintf("%d", t.MaxIterations), fmt.Sprintf("%t", t.Dynamic)}, "\x00")
	}
	sameSet := func(a, b []string) bool {
		if len(a) != len(b) {
			return false
		}
		m := make(map[string]int, len(a))
		for _, k := range a {
			m[k]++
		}
		for _, k := range b {
			m[k]--
			if m[k] < 0 {
				return false
			}
		}
		return true
	}
	wa, oa := make([]string, len(w.States)), make([]string, len(other.States))
	for i, s := range w.States {
		wa[i] = stateKey(s)
	}
	for i, s := range other.States {
		oa[i] = stateKey(s)
	}
	if !sameSet(wa, oa) {
		return false
	}
	wt, ot := make([]string, len(w.Transitions)), make([]string, len(other.Transitions))
	for i, t := range w.Transitions {
		wt[i] = transKey(t)
	}
	for i, t := range other.Transitions {
		ot[i] = transKey(t)
	}
	return sameSet(wt, ot)
}

// ParseBody parses just the fenced ```yaml workflow block from a story BODY (no
// skill frontmatter required) into states + transitions — for callers that have
// a story whose `## Workflow` section pins the workflow but not a full skill file.
func ParseBody(body []byte) (*Workflow, error) {
	yamlBlock, err := ExtractYAMLBlock(body)
	if err != nil {
		return nil, err
	}
	var w Workflow
	if err := yaml.Unmarshal(yamlBlock, &w); err != nil {
		return nil, fmt.Errorf("workflow: yaml block: %w", err)
	}
	if err := w.validate(); err != nil {
		return nil, err
	}
	return &w, nil
}

// hasState reports whether s is a declared state.
func (w *Workflow) hasState(s string) bool {
	for _, st := range w.States {
		if st.Name == s {
			return true
		}
	}
	return false
}

// ValidateLifecycle checks that the engagement lifecycle's derivations are
// well-defined for this workflow (sty_1604064f, epic:process-adherence). It
// fails LOUDLY at workflow-edit time when a re-authored workflow would break the
// lifecycle, so drift is a gated event rather than a silent mis-gate later:
//
//   - no terminal state (every state has an outgoing edge — a cycle): a story can
//     never reach a terminal status, so close-on-terminal is undefined; or
//   - no identifiable initial state; or
//   - the initial state is itself terminal (no transitions at all): no editable
//     phase is derivable and no story could ever progress under it.
//
// Everything is derived from the parsed shape — no hard-coded state names — so any
// workflow with a clear begin → work → end shape passes. An empty editable set
// ALONE is allowed (e.g. an epic's backlog→done parent workflow has no work
// phase); only a genuinely degenerate workflow is rejected.
func (w *Workflow) ValidateLifecycle() error {
	if len(w.States) == 0 {
		return fmt.Errorf("workflow has no states")
	}
	if len(w.TerminalStates()) == 0 {
		return fmt.Errorf("no terminal state — every state has an outgoing transition (a cycle); a story can never reach a terminal status, so the engagement lifecycle cannot determine when to close-on-terminal")
	}
	init := w.InitialState()
	if init == "" {
		return fmt.Errorf("no initial state — every state has an incoming transition; the entry point is undefined")
	}
	if w.IsTerminal(init) {
		return fmt.Errorf("initial state %q is also terminal — the workflow has no transitions, so no editable phase is derivable and no story could progress under it", init)
	}
	return nil
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
