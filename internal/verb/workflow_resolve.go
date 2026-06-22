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
