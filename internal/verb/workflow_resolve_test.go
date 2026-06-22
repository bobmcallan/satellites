package verb

import (
	"strings"
	"testing"
)

// A minimal product-style workflow source (applies_to ["*"]) and a skills-repo
// style one (applies_to ["skill"]) — the two-workflow corpus order-5 resolves.
const productWFBody = "---\nname: prod-wf\nkind: workflow\napplies_to: [\"*\"]\n---\n" +
	"# Prod\n\n```yaml\nstates:\n  - backlog\n  - ready\n  - {name: in_progress, actor: executor}\n  - {name: done-review, actor: reviewer}\n  - done\ntransitions:\n  - {from: backlog, to: ready, reviewer_skill: \"plan\"}\n  - {from: ready, to: in_progress, reviewer_skill: \"start\"}\n  - {from: in_progress, to: done-review, trigger: checkpoint}\n  - {from: done-review, on: pass, to: done, reviewer_skill: \"done\"}\n  - {from: done-review, on: fail, to: in_progress, max_iterations: 3, on_exhausted: in_progress, reviewer_skill: \"done\"}\n```\n"

const skillsWFBody = "---\nname: skills-wf\nkind: workflow\napplies_to: [\"skill\"]\n---\n" +
	"# Skills\n\n```yaml\nstates:\n  - backlog\n  - ready\n  - {name: in_progress, actor: executor}\n  - {name: publish-review, actor: reviewer}\n  - done\ntransitions:\n  - {from: backlog, to: ready, reviewer_skill: \"plan\"}\n  - {from: ready, to: in_progress, reviewer_skill: \"start\"}\n  - {from: in_progress, to: publish-review, trigger: checkpoint}\n  - {from: publish-review, on: pass, to: done, reviewer_skill: \"pub\"}\n  - {from: publish-review, on: fail, to: in_progress, max_iterations: 3, on_exhausted: in_progress, reviewer_skill: \"pub\"}\n```\n"

func sources() []WorkflowSource {
	return []WorkflowSource{{Name: "prod-wf", Body: productWFBody}, {Name: "skills-wf", Body: skillsWFBody}}
}

func TestResolveGoverningWorkflow(t *testing.T) {
	// AC1: a specific category resolves the specific workflow over the wildcard.
	if _, name, ok := ResolveGoverningWorkflow("skill", sources()); !ok || name != "skills-wf" {
		t.Fatalf("category=skill resolved %q ok=%v, want skills-wf", name, ok)
	}
	// A category only the wildcard covers resolves the product workflow.
	if _, name, ok := ResolveGoverningWorkflow("infrastructure", sources()); !ok || name != "prod-wf" {
		t.Fatalf("category=infrastructure resolved %q ok=%v, want prod-wf", name, ok)
	}
	// Case-insensitive specific match.
	if _, name, ok := ResolveGoverningWorkflow("SKILL", sources()); !ok || name != "skills-wf" {
		t.Fatalf("category=SKILL resolved %q, want skills-wf", name)
	}
	// No workflows at all → ungoverned (ok=false).
	if _, _, ok := ResolveGoverningWorkflow("skill", nil); ok {
		t.Fatal("no sources should resolve ungoverned (ok=false)")
	}
	// No wildcard present and no specific match → ungoverned.
	only := []WorkflowSource{{Name: "skills-wf", Body: skillsWFBody}}
	if _, _, ok := ResolveGoverningWorkflow("feature", only); ok {
		t.Fatal("category=feature with only a skill-scoped workflow should be ungoverned")
	}
}

func TestExplainTransition(t *testing.T) {
	cat := "feature" // covered by prod-wf's wildcard applies_to

	// v2 edge, gate matches: done-review → done on pass, gate "done".
	r := ExplainTransition("", productWFBody, "done-review", cat, "done", sources())
	if r.Governing != "prod-wf" {
		t.Fatalf("governing=%q, want prod-wf", r.Governing)
	}
	if len(r.Edges) != 2 {
		t.Fatalf("done-review edges = %d, want 2 (pass+fail)", len(r.Edges))
	}
	if r.Edges[0].Model != "v2-client-enact" {
		t.Errorf("pass edge model = %q, want v2-client-enact", r.Edges[0].Model)
	}
	if !strings.Contains(r.Verdict, "v2 pass/fail") || !strings.Contains(r.Verdict, "CLIENT enacts") {
		t.Errorf("v2 verdict unexpected: %q", r.Verdict)
	}

	// v1 gated edge: backlog → ready, gate "plan" (no on:) — client-enacted.
	r = ExplainTransition("", productWFBody, "backlog", cat, "plan", sources())
	if r.Edges[0].Model != "v1-client-enact" {
		t.Errorf("backlog edge model = %q, want v1-client-enact", r.Edges[0].Model)
	}
	if !strings.Contains(r.Verdict, "CLIENT enacts") || !strings.Contains(r.Verdict, "judges only") {
		t.Errorf("v1 verdict unexpected: %q", r.Verdict)
	}

	// Gate names no edge from here — the edge's real gate is surfaced.
	r = ExplainTransition("", productWFBody, "done-review", cat, "wrong-gate", sources())
	if !strings.Contains(r.Verdict, "governs no edge") || !strings.Contains(r.Verdict, "\"done\"") {
		t.Errorf("mismatch verdict should name the real gate: %q", r.Verdict)
	}

	// Checkpoint state: in_progress advances by --checkpoint, a gate enacts nothing.
	r = ExplainTransition("", productWFBody, "in_progress", cat, "done", sources())
	if !strings.Contains(r.Verdict, "--checkpoint") {
		t.Errorf("checkpoint verdict should steer to --checkpoint: %q", r.Verdict)
	}

	// Terminal state: no outgoing edge.
	r = ExplainTransition("", productWFBody, "done", cat, "done", sources())
	if len(r.Edges) != 0 || !strings.Contains(r.Verdict, "terminal") {
		t.Errorf("terminal state verdict unexpected: edges=%d verdict=%q", len(r.Edges), r.Verdict)
	}

	// Dangling selector fails closed with drift, no edges.
	r = ExplainTransition("no-such-wf", productWFBody, "backlog", cat, "plan", sources())
	if r.Drift == "" || len(r.Edges) != 0 {
		t.Errorf("dangling selector should fail closed: drift=%q edges=%d", r.Drift, len(r.Edges))
	}
}

func TestGoverningReconcile_Conformant(t *testing.T) {
	// A skill-category story embedding the skills workflow verbatim: enacts the
	// skills workflow's edges from publish-review, no drift.
	edges, governing, drift := GoverningReconcile("", skillsWFBody, "publish-review", "skill", sources())
	if governing != "skills-wf" {
		t.Fatalf("governing=%q, want skills-wf", governing)
	}
	if drift != "" {
		t.Fatalf("conformant embedded copy should not drift, got %q", drift)
	}
	if !edges.IsV2 || edges.PassTo != "done" || edges.FailTo != "in_progress" {
		t.Fatalf("publish-review edges = %+v, want pass=done fail=in_progress", edges)
	}
}

func TestGoverningReconcile_DriftSurfaced(t *testing.T) {
	// A skill-category story that embedded the PRODUCT workflow (a divergent
	// copy): the governing skills workflow is resolved, its edges enacted, and
	// the divergence is surfaced as drift — the embedded copy is not honoured.
	edges, governing, drift := GoverningReconcile("", productWFBody, "in_progress", "skill", sources())
	if governing != "skills-wf" {
		t.Fatalf("governing=%q, want skills-wf (resolved by category, not the embedded copy)", governing)
	}
	if drift == "" {
		t.Fatal("divergent embedded copy must surface drift")
	}
	// The enacted checkpoint target is the governing workflow's (publish-review),
	// not the embedded product copy's (done-review).
	to, ok := GoverningCheckpoint("", productWFBody, "in_progress", "skill", sources())
	if !ok || to != "publish-review" {
		t.Fatalf("checkpoint enacted %q ok=%v, want publish-review (the governing workflow's)", to, ok)
	}
	_ = edges
}

func TestGoverningReconcile_UngovernedFallsBackToEmbedded(t *testing.T) {
	// No workflow covers the category → the embedded copy is used (legacy path).
	only := []WorkflowSource{{Name: "skills-wf", Body: skillsWFBody}}
	edges, governing, drift := GoverningReconcile("", productWFBody, "done-review", "feature", only)
	if governing != "" || drift != "" {
		t.Fatalf("ungoverned category should fall back silently, got governing=%q drift=%q", governing, drift)
	}
	if !edges.IsV2 || edges.PassTo != "done" {
		t.Fatalf("embedded edges = %+v, want pass=done", edges)
	}
}

// TestWorkflowSelector: the chosen workflow name is read from a `workflow:<name>`
// tag; other tags and absence are handled (sty_cfbcc6e2).
func TestWorkflowSelector(t *testing.T) {
	if got := WorkflowSelector([]string{"area:substrate", "workflow:skills-wf", "epic-order:3"}); got != "skills-wf" {
		t.Fatalf("selector = %q, want skills-wf", got)
	}
	if got := WorkflowSelector([]string{"area:substrate"}); got != "" {
		t.Fatalf("no workflow tag should yield empty selector, got %q", got)
	}
}

// TestResolveByName: resolves by frontmatter name and reports category coverage;
// a dangling name is not ok (sty_cfbcc6e2 AC3).
func TestResolveByName(t *testing.T) {
	// skills-wf covers "skill" (specific), not "feature".
	if wf, covers, ok := ResolveByName("skills-wf", "skill", sources()); !ok || !covers || wf == nil {
		t.Fatalf("skills-wf/skill: ok=%v covers=%v", ok, covers)
	}
	if _, covers, ok := ResolveByName("skills-wf", "feature", sources()); !ok || covers {
		t.Fatalf("skills-wf/feature: want ok=true covers=false, got ok=%v covers=%v", ok, covers)
	}
	// prod-wf is wildcard → covers any category.
	if _, covers, ok := ResolveByName("prod-wf", "feature", sources()); !ok || !covers {
		t.Fatalf("prod-wf/feature (wildcard): want ok=true covers=true, got ok=%v covers=%v", ok, covers)
	}
	// A dangling selector resolves nothing.
	if _, _, ok := ResolveByName("nope-wf", "skill", sources()); ok {
		t.Fatalf("dangling selector must not resolve")
	}
}

// TestGoverningReconcile_SelectorAuthoritative: a valid selector loads THAT
// workflow's edges (not category resolution, not the embedded copy) with no
// drift; a dangling or non-matching selector fails closed — empty edges + drift
// (sty_cfbcc6e2 AC1/AC3).
func TestGoverningReconcile_SelectorAuthoritative(t *testing.T) {
	// Selector names prod-wf (wildcard) for a skill story: the selector wins over
	// the category default (skills-wf). Edges come from prod-wf; no drift.
	edges, governing, drift := GoverningReconcile("prod-wf", skillsWFBody, "done-review", "skill", sources())
	if governing != "prod-wf" || drift != "" {
		t.Fatalf("selector prod-wf: governing=%q drift=%q, want prod-wf/no-drift", governing, drift)
	}
	if !edges.IsV2 || edges.PassTo != "done" {
		t.Fatalf("prod-wf done-review edges = %+v, want v2 pass=done", edges)
	}

	// Dangling selector → fail closed (empty edges, drift names the gap).
	edges, _, drift = GoverningReconcile("ghost-wf", skillsWFBody, "in_progress", "skill", sources())
	if drift == "" || edges.IsV2 {
		t.Fatalf("dangling selector must fail closed, got edges=%+v drift=%q", edges, drift)
	}

	// Non-matching selector (skills-wf does not cover feature) → fail closed.
	edges, _, drift = GoverningReconcile("skills-wf", skillsWFBody, "in_progress", "feature", sources())
	if drift == "" || edges.IsV2 {
		t.Fatalf("non-matching selector must fail closed, got edges=%+v drift=%q", edges, drift)
	}
}
