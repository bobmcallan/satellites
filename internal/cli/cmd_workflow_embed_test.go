package cli

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/verb"
)

// govWFBody is a product-style governing workflow (applies_to ["*"]).
const govWFBody = "---\nname: gov-wf\nkind: workflow\napplies_to: [\"*\"]\n---\n" +
	"# Gov\n\n```yaml\nstates:\n  - backlog\n  - {name: plan-reviewed, actor: reviewer}\n  - ready\n  - {name: in_progress, actor: executor}\n  - {name: done-review, actor: reviewer}\n  - done\ntransitions:\n  - {from: backlog, to: plan-reviewed, reviewer_skill: \"plan\"}\n  - {from: plan-reviewed, to: ready, reviewer_skill: \"intent\"}\n  - {from: ready, to: in_progress, reviewer_skill: \"start\"}\n  - {from: in_progress, to: done-review, trigger: checkpoint}\n  - {from: done-review, on: pass, to: done, reviewer_skill: \"done\"}\n  - {from: done-review, on: fail, to: in_progress, max_iterations: 3, on_exhausted: in_progress, reviewer_skill: \"done\"}\n```\n"

// staleEmbedBody is a story whose embedded `## Workflow` is an OLDER shape of
// the governing workflow (missing the plan-reviewed state) — the divergence a
// governing-config edit produces on an in-flight story.
const staleEmbedBody = "## Purpose\n\nDo the thing.\n\n## Workflow\n\n```yaml\n" +
	"states:\n  - backlog\n  - ready\n  - {name: in_progress, actor: executor}\n  - {name: done-review, actor: reviewer}\n  - done\ntransitions:\n  - {from: backlog, to: ready, reviewer_skill: \"plan\"}\n  - {from: ready, to: in_progress, reviewer_skill: \"start\"}\n  - {from: in_progress, to: done-review, trigger: checkpoint}\n  - {from: done-review, on: pass, to: done, reviewer_skill: \"done\"}\n  - {from: done-review, on: fail, to: in_progress, max_iterations: 3, on_exhausted: in_progress, reviewer_skill: \"done\"}\n```\n\n## Acceptance criteria\n\n1. Works.\n"

func embedTestSources() []verb.WorkflowSource {
	return []verb.WorkflowSource{{Name: "gov-wf", Body: govWFBody}}
}

// sty_ba0d1ab7 (epic-order:2.9): a story whose embedded `## Workflow` diverges
// from the governing workflow self-heals — syncEmbeddedWorkflowBody re-stamps it
// to the governing definition, after which GoverningReconcile reports no drift,
// and the resync is idempotent.
func TestSyncEmbeddedWorkflowBody_ReSyncsAndClearsDrift(t *testing.T) {
	sources := embedTestSources()

	// Precondition: the stale embed diverges → GoverningReconcile surfaces drift.
	if _, gov, drift := verb.GoverningReconcile("", staleEmbedBody, "in_progress", "feature", sources); gov != "gov-wf" || drift == "" {
		t.Fatalf("precondition: want governing=gov-wf with drift, got governing=%q drift=%q", gov, drift)
	}

	// Re-stamp from the governing definition.
	synced, changed, gov, ok, err := syncEmbeddedWorkflowBody(staleEmbedBody, "feature", sources)
	if err != nil || !ok || !changed {
		t.Fatalf("sync want changed+ok, got changed=%v ok=%v err=%v", changed, ok, err)
	}
	if gov != "gov-wf" {
		t.Fatalf("governing=%q, want gov-wf", gov)
	}
	// The non-workflow prose is preserved; only the ## Workflow section changed.
	if !strings.Contains(synced, "## Purpose") || !strings.Contains(synced, "## Acceptance criteria") {
		t.Fatalf("resync dropped surrounding sections:\n%s", synced)
	}
	if !strings.Contains(synced, "plan-reviewed") {
		t.Fatalf("resynced embed missing the governing workflow's plan-reviewed state:\n%s", synced)
	}

	// AC1/AC2: after the resync, GoverningReconcile reports NO drift — the embed
	// now mirrors the authoritative governing definition.
	if _, gov2, drift := verb.GoverningReconcile("", synced, "in_progress", "feature", sources); gov2 != "gov-wf" || drift != "" {
		t.Fatalf("post-resync want governing=gov-wf no drift, got governing=%q drift=%q", gov2, drift)
	}

	// Idempotent: a second resync of an already-synced body changes nothing.
	if _, changed2, _, ok2, err2 := syncEmbeddedWorkflowBody(synced, "feature", sources); err2 != nil || !ok2 || changed2 {
		t.Fatalf("second sync want no change, got changed=%v ok=%v err=%v", changed2, ok2, err2)
	}
}

// An ungoverned category (no governing workflow covers it) keeps the embed as
// its own source — sync is a no-op (ok=false), never an error.
func TestSyncEmbeddedWorkflowBody_UngovernedNoOp(t *testing.T) {
	only := []verb.WorkflowSource{{Name: "skill-only", Body: strings.Replace(govWFBody, "applies_to: [\"*\"]", "applies_to: [\"skill\"]", 1)}}
	body, changed, gov, ok, err := syncEmbeddedWorkflowBody(staleEmbedBody, "feature", only)
	if err != nil || ok || changed || gov != "" {
		t.Fatalf("ungoverned want no-op (ok=false), got changed=%v ok=%v gov=%q err=%v", changed, ok, gov, err)
	}
	if body != staleEmbedBody {
		t.Fatal("ungoverned sync must not rewrite the body")
	}
}
