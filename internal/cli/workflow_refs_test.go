package cli

import "testing"

const refTestWorkflow = "---\n" +
	"name: wf\n" +
	"kind: workflow\n" +
	"applies_to: [infrastructure]\n" +
	"---\n" +
	"Run the [[satellites-commit-push]] capability. See [[reviewer-only-model]].\n" +
	"\n" +
	"```yaml\n" +
	"states:\n" +
	"  - backlog\n" +
	"  - {name: in_progress, actor: executor}\n" +
	"  - done\n" +
	"transitions:\n" +
	"  - {from: backlog, to: in_progress, reviewer_skill: \"satellites-intent-plan-review\"}\n" +
	"  - {from: in_progress, to: done, reviewer_skill: \"ghost-review\"}\n" +
	"```\n"

// TestReviewWorkflowRefs pins the deterministic dry-run: an embedded gate
// (resolveSkill true) and a principle wikilink (resolveDoc true) pass; a
// reviewer_skill resolving from no tier and a prose wikilink to a deleted
// capability are both flagged — the [[satellites-commit-push]]-class orphan
// `workflow check` misses.
func TestReviewWorkflowRefs(t *testing.T) {
	resolveSkill := func(n string) bool { return n == "satellites-intent-plan-review" }
	resolveDoc := func(n string) bool { return n == "reviewer-only-model" }

	findings := reviewWorkflowRefs(refTestWorkflow, resolveSkill, resolveDoc)

	got := map[string]string{} // text → rule
	for _, f := range findings {
		got[f.Text] = f.Rule
	}
	if len(got) != 2 {
		t.Fatalf("want 2 findings, got %d: %#v", len(got), findings)
	}
	if got["ghost-review"] != "unresolvable-reviewer" {
		t.Errorf("ghost-review: want unresolvable-reviewer, got %q", got["ghost-review"])
	}
	if got["satellites-commit-push"] != "dangling-reference" {
		t.Errorf("satellites-commit-push: want dangling-reference, got %q", got["satellites-commit-push"])
	}
	if _, flagged := got["satellites-intent-plan-review"]; flagged {
		t.Errorf("embedded gate satellites-intent-plan-review must NOT be flagged")
	}
	if _, flagged := got["reviewer-only-model"]; flagged {
		t.Errorf("principle wikilink reviewer-only-model must NOT be flagged")
	}
}

// TestReviewWorkflowRefs_AllResolve: a workflow whose every reference resolves
// produces no findings.
func TestReviewWorkflowRefs_AllResolve(t *testing.T) {
	all := func(string) bool { return true }
	if f := reviewWorkflowRefs(refTestWorkflow, all, all); len(f) != 0 {
		t.Fatalf("want clean, got %#v", f)
	}
}

// TestReconcileWorkflows pins the reconcile verdicts that matter for the flat
// .satellites/workflows layout: an operator-authored (unstamped, no substrate
// row) workflow is LEFT untouched, never removed; a substrate row with no local
// copy installs.
func TestReconcileWorkflows(t *testing.T) {
	subs := []substrateSkill{{Name: "wf-server", DocumentID: "doc1", Version: 1, Body: "body"}}
	locals := []localSkill{{Name: "wf-local", Stamped: false}}

	plan := reconcileWorkflows(subs, locals)
	got := map[string]syncAction{}
	for _, p := range plan {
		got[p.Name] = p.Action
	}
	if got["wf-server"] != actionInstall {
		t.Errorf("wf-server: want install, got %v", got["wf-server"])
	}
	// An unstamped local with no substrate row is operator-authored — omitted from
	// the plan entirely (reconcileWorkflows only includes stamped locals or
	// substrate names), so it is never removed.
	if _, present := got["wf-local"]; present {
		t.Errorf("operator-authored wf-local must not appear in the plan (got %v) — it must be left untouched", got["wf-local"])
	}
}
