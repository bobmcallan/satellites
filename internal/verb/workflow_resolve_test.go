package verb

import "testing"

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

func TestGoverningReconcile_Conformant(t *testing.T) {
	// A skill-category story embedding the skills workflow verbatim: enacts the
	// skills workflow's edges from publish-review, no drift.
	edges, governing, drift := GoverningReconcile(skillsWFBody, "publish-review", "skill", sources())
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
	edges, governing, drift := GoverningReconcile(productWFBody, "in_progress", "skill", sources())
	if governing != "skills-wf" {
		t.Fatalf("governing=%q, want skills-wf (resolved by category, not the embedded copy)", governing)
	}
	if drift == "" {
		t.Fatal("divergent embedded copy must surface drift")
	}
	// The enacted checkpoint target is the governing workflow's (publish-review),
	// not the embedded product copy's (done-review).
	to, ok := GoverningCheckpoint(productWFBody, "in_progress", "skill", sources())
	if !ok || to != "publish-review" {
		t.Fatalf("checkpoint enacted %q ok=%v, want publish-review (the governing workflow's)", to, ok)
	}
	_ = edges
}

func TestGoverningReconcile_UngovernedFallsBackToEmbedded(t *testing.T) {
	// No workflow covers the category → the embedded copy is used (legacy path).
	only := []WorkflowSource{{Name: "skills-wf", Body: skillsWFBody}}
	edges, governing, drift := GoverningReconcile(productWFBody, "done-review", "feature", only)
	if governing != "" || drift != "" {
		t.Fatalf("ungoverned category should fall back silently, got governing=%q drift=%q", governing, drift)
	}
	if !edges.IsV2 || edges.PassTo != "done" {
		t.Fatalf("embedded edges = %+v, want pass=done", edges)
	}
}
