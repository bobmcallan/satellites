package cli

import (
	"testing"

	"github.com/bobmcallan/satellites/internal/verb"
)

// TestEmbeddedWorkflowsLoaded: the config/workflows embed projects into the
// resolver's source set — the baseline (wildcard) and parent (specific)
// defaults are present, so they can govern a repo with no .satellites/workflows
// (sty_6c6056f9, AC1).
func TestEmbeddedWorkflowsLoaded(t *testing.T) {
	got := embeddedWorkflows()
	if len(got) < 2 {
		t.Fatalf("embeddedWorkflows() returned %d, want >= 2 (baseline + parent)", len(got))
	}
	byName := map[string]matSkill{}
	for _, s := range got {
		if s.kind != "workflow" {
			t.Errorf("embed workflow %q has kind %q, want workflow", s.name, s.kind)
		}
		if s.scope != "system" {
			t.Errorf("embed workflow %q has scope %q, want system", s.name, s.scope)
		}
		byName[s.name] = s
	}
	for _, want := range []string{"satellites-baseline-workflow", "satellites-parent-workflow"} {
		if _, ok := byName[want]; !ok {
			t.Errorf("embed missing default workflow %q", want)
		}
	}
}

// TestEmbedGovernsWithoutClientDir: with NO .satellites/workflows files, the
// embed alone still governs — infrastructure resolves the wildcard baseline and
// parent resolves the specific parent workflow (sty_6c6056f9, AC3).
func TestEmbedGovernsWithoutClientDir(t *testing.T) {
	src := embeddedWorkflowSources()
	if _, name, ok := verb.ResolveGoverningWorkflow("infrastructure", src); !ok || name != "satellites-baseline-workflow" {
		t.Fatalf("infrastructure resolved %q ok=%v over embed-only, want satellites-baseline-workflow", name, ok)
	}
	if _, name, ok := verb.ResolveGoverningWorkflow("parent", src); !ok || name != "satellites-parent-workflow" {
		t.Fatalf("parent resolved %q ok=%v over embed-only, want satellites-parent-workflow", name, ok)
	}
}

// TestFreshRepoNoUngovernedStory: the `workflow check` corpus of a repo with no
// client-dir workflows (withEmbeddedWorkflows(nil) = embed only) governs both a
// wildcard-covered and a specific-covered story — no ungoverned-story finding
// (sty_6c6056f9, AC3).
func TestFreshRepoNoUngovernedStory(t *testing.T) {
	corpus := withEmbeddedWorkflows(nil)
	stories := []storyLite{
		{ID: "sty_infra", Category: "infrastructure", Status: "backlog"},
		{ID: "sty_parent", Category: "parent", Status: "backlog"},
	}
	for _, f := range runWorkflowChecks(nil, corpus, stories) {
		if f.Severity == "block" && f.Code == "ungoverned-story" {
			t.Errorf("embed should govern story %q, got ungoverned-story: %s", f.Artifact, f.Message)
		}
	}
}

// TestLocalWinsOverEmbed: a client-dir workflow with the same name as an
// embedded default is NOT duplicated by the merge — local wins, the embed copy
// is dropped (sty_6c6056f9, AC2).
func TestLocalWinsOverEmbed(t *testing.T) {
	local := []matSkill{{name: "satellites-baseline-workflow", kind: "workflow", scope: "project"}}
	merged := withEmbeddedWorkflows(local)
	count := 0
	for _, s := range merged {
		if s.name == "satellites-baseline-workflow" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("satellites-baseline-workflow appears %d times after merge, want 1 (local wins)", count)
	}
	for _, s := range merged {
		if s.name == "satellites-baseline-workflow" && s.scope != "project" {
			t.Errorf("merged baseline scope %q, want project (the local copy)", s.scope)
		}
	}
}
