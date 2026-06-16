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
func GoverningReconcile(storyBody, status, category string, sources []WorkflowSource) (edges V2Edges, governing string, drift string) {
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

// GoverningCheckpoint resolves the single ungated `trigger: checkpoint` edge
// out of `status` in the AUTHORITATIVE governing workflow (mirrors
// CheckpointEdge but over the resolved definition, not the embedded copy).
// Falls back to the embedded copy when no workflow covers the category.
func GoverningCheckpoint(storyBody, status, category string, sources []WorkflowSource) (string, bool) {
	auth, _, ok := ResolveGoverningWorkflow(category, sources)
	if !ok {
		return CheckpointEdge(storyBody, status)
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
func GoverningUngatedAdvance(storyBody, status, category, target string, sources []WorkflowSource) bool {
	var froms []workflow.Transition
	if auth, _, ok := ResolveGoverningWorkflow(category, sources); ok {
		froms = auth.TransitionsFrom(strings.TrimSpace(status))
	} else if embedded, err := workflow.ParseBody([]byte(storyBody)); err == nil && embedded != nil {
		froms = embedded.TransitionsFrom(strings.TrimSpace(status))
	}
	target = strings.TrimSpace(target)
	for _, t := range froms {
		if t.To == target && strings.TrimSpace(t.ReviewerSkill) == "" && t.On == "" {
			return true
		}
	}
	return false
}
