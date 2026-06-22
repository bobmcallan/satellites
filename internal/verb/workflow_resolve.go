// Config-driven governing-workflow resolution (epic:skills-registry order-5,
// sty_0889de7a). The gate must trust the RESOLVED governing workflow — the one
// the registry says applies to this story's category — not whatever `##
// Workflow` a story embedded, so an embedded copy cannot silently weaken the
// gates. Selection is pure configuration: it resolves by `applies_to` ↔ story
// category over the materialised (scope-cascade-resolved) workflow set, with no
// filename binding and no hardcoded workflow name. Pure (no I/O) so the
// dispatcher's behaviour stays fixture-testable; the CLI passes in the
// materialised sources.

package verb

import (
	"fmt"
	"strings"

	"github.com/bobmcallan/satellites/internal/workflow"
)

// WorkflowSource is one materialised `kind:workflow` skill the resolver
// considers — its name and its full SKILL.md body (frontmatter carries
// `applies_to`; the ```yaml block carries the state machine). The caller
// passes the `.claude/skills` set, which sync has already reconciled by scope
// precedence (project > workspace > system > library-pin), so resolving over it
// honours the cascade.
type WorkflowSource struct {
	Name string
	Body string
}

// WorkflowSelectorTag is the story-tag prefix recording the CHOSEN governing
// workflow BY NAME (sty_cfbcc6e2). A story tagged `workflow:<name>` is governed
// by that named workflow — resolved from the source set, not by category
// auto-resolution and never by its embedded ## Workflow copy (tamper-proof, no
// drift). The selector is the authority; the embedded YAML is display-only.
const WorkflowSelectorTag = "workflow:"

// WorkflowSelector returns the chosen workflow name from a story's tags (the
// value of the first `workflow:<name>` tag), or "" when none is set. Pure.
func WorkflowSelector(tags []string) string {
	for _, t := range tags {
		if v, ok := strings.CutPrefix(strings.TrimSpace(t), WorkflowSelectorTag); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// appliesToCovers reports whether a workflow's `applies_to` covers a category —
// a specific (case-insensitive) match or the "*" wildcard. Pure.
func appliesToCovers(wf *workflow.Workflow, category string) bool {
	category = strings.TrimSpace(category)
	for _, at := range wf.AppliesTo {
		at = strings.TrimSpace(at)
		if at == "*" || (category != "" && strings.EqualFold(at, category)) {
			return true
		}
	}
	return false
}

// ResolveByName resolves the workflow a selector records — the source whose
// frontmatter (or projected) name matches `name` — and reports whether it covers
// `category`. ok is false when NO source carries that name (the selector is
// dangling); covers is false when the named workflow's applies_to does not cover
// the category (a non-matching selector). Both are fail-closed conditions the
// caller surfaces (the engine reports, the plan gate rejects — sty_cfbcc6e2 AC3).
// Pure (no I/O).
func ResolveByName(name, category string, sources []WorkflowSource) (wf *workflow.Workflow, covers bool, ok bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, false, false
	}
	for _, s := range sources {
		parsed, err := workflow.Parse([]byte(s.Body))
		if err != nil || parsed == nil {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(s.Name), name) && !strings.EqualFold(strings.TrimSpace(parsed.Name), name) {
			continue
		}
		return parsed, appliesToCovers(parsed, category), true
	}
	return nil, false, false
}

// ResolveGoverningWorkflow picks the workflow whose `applies_to` covers the
// story category. An exact (case-insensitive) category match wins over a "*"
// wildcard; among wildcards or exact matches the first in source order wins
// (the caller orders sources by precedence). Returns ok=false when no workflow
// covers the category — the ungoverned/legacy case the embedded copy then owns.
func ResolveGoverningWorkflow(category string, sources []WorkflowSource) (*workflow.Workflow, string, bool) {
	category = strings.TrimSpace(category)
	var wildcard *workflow.Workflow
	var wildcardName string
	for _, s := range sources {
		wf, err := workflow.Parse([]byte(s.Body))
		if err != nil || wf == nil {
			continue
		}
		for _, at := range wf.AppliesTo {
			at = strings.TrimSpace(at)
			if at == "*" {
				if wildcard == nil {
					wildcard, wildcardName = wf, s.Name
				}
				continue
			}
			if category != "" && strings.EqualFold(at, category) {
				return wf, s.Name, true // a specific match wins immediately
			}
		}
	}
	if wildcard != nil {
		return wildcard, wildcardName, true
	}
	return nil, "", false
}

// GoverningReconcile resolves the authoritative governing workflow for the
// story's category and returns the V2 edge set to ENACT from `status`, the
// governing workflow name, and a drift message when the story's embedded `##
// Workflow` diverges from the authoritative definition.
//
// The enacted edges come from the AUTHORITATIVE workflow, never the embedded
// copy — so an embedded copy that weakens a gate is not honoured; the
// divergence is surfaced as drift instead. When no workflow covers the
// category, it falls back to the embedded copy (the legacy/ungoverned path,
// where plan-review owns the story shape).
func GoverningReconcile(selector, storyBody, status, category string, sources []WorkflowSource) (edges V2Edges, governing string, drift string) {
	// A recorded selector is the authority (sty_cfbcc6e2): load the named
	// workflow's edges, fail closed when it is dangling or non-matching. The
	// embedded ## Workflow is never consulted on the selector path — the name is
	// tamper-proof, so there is no drift to surface.
	if selector != "" {
		wf, covers, ok := ResolveByName(selector, category, sources)
		if !ok {
			return V2Edges{}, "", fmt.Sprintf("workflow selector %q names no workflow in the source set — fail closed (pick a valid one: satellites workflow list)", selector)
		}
		if !covers {
			return V2Edges{}, selector, fmt.Sprintf("workflow selector %q does not cover category %q — fail closed (pick a matching workflow)", selector, category)
		}
		return edgesFromWorkflow(wf, status), selector, ""
	}
	auth, name, ok := ResolveGoverningWorkflow(category, sources)
	if !ok {
		e, _ := ResolveV2Edges(storyBody, status)
		return e, "", ""
	}
	edges = edgesFromWorkflow(auth, status)
	if embedded, err := workflow.ParseBody([]byte(storyBody)); err == nil && embedded != nil && !auth.Equivalent(embedded) {
		drift = fmt.Sprintf("story's embedded ## Workflow diverges from the governing workflow %q (resolved by category %q) — the governing definition is enacted; the embedded copy is not honoured", name, category)
	}
	return edges, name, drift
}

// GoverningEdgesHint names every transition out of `status` in the AUTHORITATIVE
// governing workflow (selector first, else the category default, else the story's
// embedded copy), each with the --skill <gate> (or --checkpoint) that drives it.
// It is the discoverability fix behind sty_4300e117: an agent stuck at `running`
// could not see that satellites-task-report-review drives running→complete and
// hand-stamped the task instead. Returns "" when no edge resolves; otherwise a
// clause that appends to a dead-end error. The formatting lives here (not the CLI)
// because the CLI layering guard forbids importing internal/workflow.
func GoverningEdgesHint(selector, storyBody, status, category string, sources []WorkflowSource) string {
	var auth *workflow.Workflow
	if selector != "" {
		if wf, covers, ok := ResolveByName(selector, category, sources); ok && covers {
			auth = wf
		}
	}
	if auth == nil {
		if w, _, ok := ResolveGoverningWorkflow(category, sources); ok {
			auth = w
		} else if embedded, err := workflow.ParseBody([]byte(storyBody)); err == nil {
			auth = embedded
		}
	}
	if auth == nil {
		return ""
	}
	edges := auth.TransitionsFrom(strings.TrimSpace(status))
	if len(edges) == 0 {
		return ""
	}
	var parts []string
	for _, t := range edges {
		switch {
		case strings.TrimSpace(t.ReviewerSkill) != "":
			parts = append(parts, fmt.Sprintf("→ %s (--skill %s)", t.To, strings.TrimSpace(t.ReviewerSkill)))
		case t.Trigger == "checkpoint":
			parts = append(parts, fmt.Sprintf("→ %s (--checkpoint)", t.To))
		default:
			parts = append(parts, fmt.Sprintf("→ %s", t.To))
		}
	}
	return fmt.Sprintf(" — from %q the governing workflow's transitions are: %s", strings.TrimSpace(status), strings.Join(parts, " · "))
}

// ExplainEdge is one outgoing transition from a status, flattened for the
// `status_transition --explain` dry-run (sty_550e9595).
type ExplainEdge struct {
	To      string // target status
	On      string // "pass" | "fail" | "" (unconditional)
	Trigger string // "checkpoint" | ""
	Gate    string // reviewer_skill driving the edge; "" when ungated
	// Model is how the edge ENACTS. Every gated edge is client-enacted — the gate
	// JUDGES and the client writes the transition (epic:enactment-convergence
	// retired legacy-self-enact, where the gate wrote its own status_transition
	// and a judge-only gate stalled the story: the sty_0d183153 stall):
	//   v2-client-enact   — an on:pass/fail edge; the CLIENT enacts when the
	//                        invoked gate matches the edge's reviewer_skill.
	//   v1-client-enact   — an unconditional {from,to,reviewer_skill} edge; the
	//                        CLIENT enacts on the named gate's accept (a reject
	//                        records its verdict and stays put).
	//   checkpoint        — the executor's --checkpoint move (no gate).
	//   ungated           — an edge with no gate, trigger, or on: condition.
	Model string
}

// ExplainResult is the read-only resolution of a status_transition for
// `--explain`: the governing workflow as ENACTMENT would resolve it (selector
// first, else category default, else the embedded copy), the outgoing edges
// from the current status, and a plain verdict on whether the named gate can
// enact one. It mutates nothing (sty_550e9595).
type ExplainResult struct {
	Governing string // resolved workflow name ("" = none / embedded-only)
	Drift     string // selector fail-closed or embedded-vs-governing divergence
	Status    string
	Actor     string
	Edges     []ExplainEdge
	Gate      string // the gate the caller named (may be empty)
	Verdict   string // plain-language enactability verdict for Gate from Status
}

// ExplainTransition resolves a status_transition without running a gate or
// mutating anything — the engine behind `--explain`. It mirrors the enactment
// path's resolution (governingWorkflowSources + selector) so what it reports is
// exactly what enactment would act on.
func ExplainTransition(selector, storyBody, status, category, gateSkill string, sources []WorkflowSource) ExplainResult {
	status = strings.TrimSpace(status)
	res := ExplainResult{Status: status, Gate: strings.TrimSpace(gateSkill)}

	var auth *workflow.Workflow
	switch {
	case selector != "":
		wf, covers, ok := ResolveByName(selector, category, sources)
		if !ok {
			res.Drift = fmt.Sprintf("workflow selector %q names no workflow in the source set — enactment fails closed", selector)
			return res
		}
		if !covers {
			res.Governing = selector
			res.Drift = fmt.Sprintf("workflow selector %q does not cover category %q — enactment fails closed", selector, category)
			return res
		}
		auth, res.Governing = wf, selector
	default:
		if w, name, ok := ResolveGoverningWorkflow(category, sources); ok {
			auth, res.Governing = w, name
			if embedded, err := workflow.ParseBody([]byte(storyBody)); err == nil && embedded != nil && !w.Equivalent(embedded) {
				res.Drift = fmt.Sprintf("story's embedded ## Workflow diverges from governing workflow %q — the governing definition is enacted, the embedded copy is not", name)
			}
		} else if embedded, err := workflow.ParseBody([]byte(storyBody)); err == nil && embedded != nil {
			auth = embedded
		}
	}
	if auth == nil {
		res.Verdict = "no governing workflow resolves for this story — nothing to enact"
		return res
	}
	if st, ok := auth.StateOf(status); ok {
		res.Actor = st.Actor
	}

	var matched *ExplainEdge
	var otherGate string
	for _, t := range auth.TransitionsFrom(status) {
		e := ExplainEdge{To: t.To, On: strings.TrimSpace(t.On), Trigger: strings.TrimSpace(t.Trigger), Gate: strings.TrimSpace(t.ReviewerSkill)}
		switch {
		case e.On == "pass" || e.On == "fail":
			e.Model = "v2-client-enact"
		case e.Gate != "":
			e.Model = "v1-client-enact"
		case e.Trigger == "checkpoint":
			e.Model = "checkpoint"
		default:
			e.Model = "ungated"
		}
		res.Edges = append(res.Edges, e)
		if e.Gate != "" && res.Gate != "" {
			if strings.EqualFold(e.Gate, res.Gate) {
				ec := e
				matched = &ec
			} else if otherGate == "" {
				otherGate = e.Gate
			}
		}
	}
	res.Verdict = explainVerdict(res, matched, otherGate)
	return res
}

// explainVerdict renders the human enactability sentence for ExplainTransition.
func explainVerdict(res ExplainResult, matched *ExplainEdge, otherGate string) string {
	if len(res.Edges) == 0 {
		return fmt.Sprintf("status %q has no outgoing transition — it is terminal; nothing to enact", res.Status)
	}
	if res.Gate == "" {
		return "no --skill named — pass a gate to see whether it enacts an edge from here"
	}
	if matched != nil {
		switch matched.Model {
		case "v2-client-enact":
			return fmt.Sprintf("gate %q governs %s → %s as a v2 pass/fail edge — on accept the CLIENT enacts the move", res.Gate, res.Status, matched.To)
		case "v1-client-enact":
			return fmt.Sprintf("gate %q governs %s → %s — on accept the CLIENT enacts the move (a reject stays put); the gate judges only", res.Gate, res.Status, matched.To)
		}
	}
	if res.Actor == "operator" {
		return fmt.Sprintf("status %q is an operator state — not the executor's turn; this gate will not enact here", res.Status)
	}
	for _, e := range res.Edges {
		if e.Model == "checkpoint" {
			return fmt.Sprintf("status %q advances by the executor's --checkpoint (→ %s), not a reviewer gate — %q enacts nothing here", res.Status, e.To, res.Gate)
		}
	}
	if otherGate != "" {
		return fmt.Sprintf("gate %q governs no edge from %q — that edge's gate is %q; running %q here judges only, nothing enacts", res.Gate, res.Status, otherGate, res.Gate)
	}
	return fmt.Sprintf("gate %q governs no edge from %q — nothing would enact", res.Gate, res.Status)
}

// GoverningCheckpoint resolves the single ungated `trigger: checkpoint` edge
// out of `status` in the AUTHORITATIVE governing workflow (mirrors
// CheckpointEdge but over the resolved definition, not the embedded copy).
// Falls back to the embedded copy when no workflow covers the category.
func GoverningCheckpoint(selector, storyBody, status, category string, sources []WorkflowSource) (string, bool) {
	var auth *workflow.Workflow
	if selector != "" {
		if wf, covers, ok := ResolveByName(selector, category, sources); ok && covers {
			auth = wf
		}
	}
	if auth == nil {
		w, _, ok := ResolveGoverningWorkflow(category, sources)
		if !ok {
			return CheckpointEdge(storyBody, status)
		}
		auth = w
	}
	to, count := "", 0
	for _, t := range auth.TransitionsFrom(strings.TrimSpace(status)) {
		if t.On != "" {
			return "", false
		}
		if t.Trigger == "checkpoint" && t.ReviewerSkill == "" {
			to, count = t.To, count+1
		}
	}
	return to, count == 1
}

// GoverningGatedEdge resolves the unconditional reviewer-gated edge
// ({from,to,reviewer_skill} with no on:/trigger) out of `status` whose
// reviewer_skill matches `gateSkill`, from the AUTHORITATIVE governing workflow
// (selector → category → embedded fallback) — the same resolution path the v2
// edges take. Returns the edge's target status. These are the edges the client
// enacts on the named gate's accept (epic:enactment-convergence — the gate
// JUDGES, the client ENACTS; there is no self-enacting gate). A state may
// declare several gated edges to different targets (e.g. backlog → in_progress
// vs backlog → cancelled), so the match is BY gateSkill — the right edge enacts
// for the gate actually run. Resolving from the governing workflow (not the
// embedded ## Workflow) means a reference-resolved workflow with no embedded
// copy (baseline / task) still enacts, and an embedded copy cannot weaken it.
func GoverningGatedEdge(selector, storyBody, status, category, gateSkill string, sources []WorkflowSource) (to string, ok bool) {
	gateSkill = strings.TrimSpace(gateSkill)
	if gateSkill == "" {
		return "", false
	}
	var auth *workflow.Workflow
	if selector != "" {
		if wf, covers, k := ResolveByName(selector, category, sources); k && covers {
			auth = wf
		}
	}
	if auth == nil {
		if w, _, k := ResolveGoverningWorkflow(category, sources); k {
			auth = w
		} else if embedded, err := workflow.ParseBody([]byte(storyBody)); err == nil && embedded != nil {
			auth = embedded
		}
	}
	if auth == nil {
		return "", false
	}
	return gatedEdgeFrom(auth, status, gateSkill)
}

// gatedEdgeFrom finds the unconditional reviewer-gated edge for `skill` out of
// `status` in a parsed workflow — an edge with no on: condition and no trigger
// whose reviewer_skill is `skill`. The shared core of GoverningGatedEdge.
func gatedEdgeFrom(wf *workflow.Workflow, status, skill string) (string, bool) {
	skill = strings.TrimSpace(skill)
	for _, t := range wf.TransitionsFrom(strings.TrimSpace(status)) {
		if t.On == "" && t.Trigger == "" && strings.EqualFold(strings.TrimSpace(t.ReviewerSkill), skill) {
			return t.To, true
		}
	}
	return "", false
}

// GoverningUngatedAdvance reports whether the AUTHORITATIVE governing workflow
// for `category` (falling back to the story's embedded copy when no workflow
// covers the category) has an UNGATED forward edge from `status` to `target` —
// an edge carrying no reviewer_skill and no on:pass|fail. This is the
// gateless-baseline advance path: such a move is enacted directly (a
// status_transition ledger row), not through a reviewer gate. A gated edge (one
// that carries a reviewer skill) is NOT advanceable this way — it must go
// through its gate; an undeclared edge returns false. Pure (no I/O) so the
// set-status surface stays fixture-testable.
func GoverningUngatedAdvance(selector, storyBody, status, category, target string, sources []WorkflowSource) bool {
	var froms []workflow.Transition
	resolved := false
	if selector != "" {
		if wf, covers, ok := ResolveByName(selector, category, sources); ok && covers {
			froms = wf.TransitionsFrom(strings.TrimSpace(status))
			resolved = true
		}
	}
	if !resolved {
		if auth, _, ok := ResolveGoverningWorkflow(category, sources); ok {
			froms = auth.TransitionsFrom(strings.TrimSpace(status))
		} else if embedded, err := workflow.ParseBody([]byte(storyBody)); err == nil && embedded != nil {
			froms = embedded.TransitionsFrom(strings.TrimSpace(status))
		}
	}
	target = strings.TrimSpace(target)
	for _, t := range froms {
		if t.To == target && strings.TrimSpace(t.ReviewerSkill) == "" && t.On == "" {
			return true
		}
	}
	return false
}
