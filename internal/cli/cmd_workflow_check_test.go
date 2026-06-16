package cli

import (
	"strings"
	"testing"
)

// Fixture corpus helpers — a minimal healthy process: one workflow naming
// one entry gate (comprehensive) + one checkpoint gate, both materialised.
func healthyCorpus() []matSkill {
	wfBody := "# wf\n\n## Checkpoint gates\n\n- [[debt-gate]] — always, pre-commit.\n\n```yaml\nstates:\n  - backlog\n  - doing\n  - done\ntransitions:\n  - {from: backlog, to: doing, reviewer_skill: \"entry-review\"}\n  - {from: doing,   to: done,  reviewer_skill: \"exit-review\"}\n```\n"
	entryBody := "Judge the story's shape, its plan, its acceptance criteria, the embedded workflow, and code grounding in one verdict.\n"
	mk := func(name, kind, scope, desc, body string) matSkill {
		extra := ""
		if kind == "workflow" {
			extra = "applies_to: [\"any\"]\n"
		}
		raw := "---\nname: " + name + "\nkind: " + kind + "\n" + extra + "description: " + desc + "\n---\n" + body
		return matSkill{name: name, kind: kind, scope: scope, description: desc, body: body, raw: raw}
	}
	return []matSkill{
		mk("the-workflow", "workflow", "", "the lifecycle", wfBody),
		mk("entry-review", "gate", "", "entry gate", entryBody),
		mk("exit-review", "gate", "", "exit gate", "verify the acceptance criteria\n"),
		mk("debt-gate", "gate", "", "pre-commit gate", "one decision rule\n"),
	}
}

// TestWorkflowCheck_AmbiguousGovernance: two non-wildcard workflows claiming
// the same story category make applies_to↔category resolution ambiguous and
// must fail closed (sty_0889de7a).
func TestWorkflowCheck_AmbiguousGovernance(t *testing.T) {
	wfRaw := func(name string) matSkill {
		body := "# " + name + "\n\n```yaml\nstates:\n  - backlog\n  - doing\n  - done\ntransitions:\n  - {from: backlog, to: doing, reviewer_skill: \"entry-review\"}\n  - {from: doing, to: done, reviewer_skill: \"exit-review\"}\n```\n"
		raw := "---\nname: " + name + "\nkind: workflow\napplies_to: [\"dup\"]\ndescription: d\n---\n" + body
		return matSkill{name: name, kind: "workflow", description: "d", body: body, raw: raw}
	}
	skills := append(healthyCorpus(), wfRaw("wf-one"), wfRaw("wf-two"))
	hit := false
	for _, f := range runWorkflowChecks(skills, nil) {
		if f.Code == "ambiguous-governance" && f.Severity == "block" && f.Artifact == "wf-one,wf-two" {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("two workflows covering category 'dup' must raise ambiguous-governance")
	}
}

// TestWorkflowCheck_CleanCorpus: a healthy corpus and governed stories yield
// zero blocking findings.
func TestWorkflowCheck_CleanCorpus(t *testing.T) {
	stories := []storyLite{{ID: "sty_1", Category: "anything", Status: "backlog",
		Body: "# s\n\n## Workflow\n\n```yaml\nstates:\n  - backlog\n  - done\ntransitions:\n  - {from: backlog, to: done, reviewer_skill: \"entry-review\"}\n```\n"}}
	for _, f := range runWorkflowChecks(healthyCorpus(), stories) {
		if f.Severity == "block" {
			t.Errorf("clean corpus produced blocking finding: %+v", f)
		}
	}
}

// TestWorkflowCheck_DriftClasses replays each pre-epic drift class against
// the pure check core (sty_11e09ae7 AC2).
func TestWorkflowCheck_DriftClasses(t *testing.T) {
	find := func(fs []driftFinding, code, artifact string) bool {
		for _, f := range fs {
			if f.Code == code && f.Artifact == artifact {
				return true
			}
		}
		return false
	}

	t.Run("class4_stamp_above_frontmatter", func(t *testing.T) {
		skills := healthyCorpus()
		skills[1].raw = "<!-- satellites-sync:begin {} satellites-sync:end -->\n" + skills[1].raw
		if !find(runWorkflowChecks(skills, nil), "unusable-skill", "entry-review") {
			t.Error("stamp above frontmatter must report unusable-skill")
		}
	})

	t.Run("class4_missing_description", func(t *testing.T) {
		skills := healthyCorpus()
		skills[2].description = ""
		if !find(runWorkflowChecks(skills, nil), "unusable-skill", "exit-review") {
			t.Error("missing description must report unusable-skill")
		}
	})

	t.Run("class6_degenerate_workflow", func(t *testing.T) {
		skills := healthyCorpus()
		skills[0].raw = "---\nname: the-workflow\nkind: workflow\ndescription: d\n---\n```yaml\nstates:\n  - only\ntransitions: []\n```\n"
		if !find(runWorkflowChecks(skills, nil), "workflow-lifecycle", "the-workflow") {
			t.Error("degenerate lifecycle must report workflow-lifecycle")
		}
	})

	t.Run("class1_shadow_gate", func(t *testing.T) {
		skills := append(healthyCorpus(), matSkill{name: "lonely-gate", kind: "gate", description: "d",
			body: "one rule\n", raw: "---\nname: lonely-gate\nkind: gate\ndescription: d\n---\none rule\n"})
		if !find(runWorkflowChecks(skills, nil), "orphan-gate", "lonely-gate") {
			t.Error("a gate no workflow names must report orphan-gate (the techdebt case)")
		}
	})

	t.Run("class5_missing_gate", func(t *testing.T) {
		skills := healthyCorpus()[:3] // drop debt-gate; the workflow's checkpoint still names it
		if !find(runWorkflowChecks(skills, nil), "missing-gate", "debt-gate") {
			t.Error("a named but unmaterialised gate must report missing-gate")
		}
	})

	t.Run("class2_gate_in_capability", func(t *testing.T) {
		skills := append(healthyCorpus(), matSkill{name: "fat-capability", kind: "capability", description: "d",
			body: "Run the linter. Exit 1 (BLOCKED) → do not publish.\n"})
		fs := runWorkflowChecks(skills, nil)
		ok := false
		for _, f := range fs {
			if f.Code == "nonatomic-candidate" && f.Artifact == "fat-capability" && f.Severity == "advise" {
				ok = true
			}
		}
		if !ok {
			t.Error("fail-closed markers in a capability must report an advisory nonatomic-candidate")
		}
	})

	t.Run("class3_host_coupled_system_scope", func(t *testing.T) {
		skills := append(healthyCorpus(), matSkill{name: "sys-skill", kind: "capability", scope: "system", description: "d",
			body: "Bump .version and watch .github/workflows/release.yml.\n"})
		if !find(runWorkflowChecks(skills, nil), "host-coupled-system", "sys-skill") {
			t.Error("repo-dev references in a system-scope skill must report host-coupled-system")
		}
	})

	t.Run("class5_ungoverned_story", func(t *testing.T) {
		stories := []storyLite{{ID: "sty_lost", Category: "portal", Status: "backlog", Body: "# no workflow here\n"}}
		skills := healthyCorpus()
		// the-workflow applies to nothing (no applies_to in fixture frontmatter) → portal uncovered
		if !find(runWorkflowChecks(skills, stories), "ungoverned-story", "sty_lost") {
			t.Error("a non-terminal story with no embedded workflow and no applies_to cover must report ungoverned-story")
		}
	})

	t.Run("class5_unresolvable_embedded_gate", func(t *testing.T) {
		stories := []storyLite{{ID: "sty_ghost", Category: "x", Status: "backlog",
			Body: "## Workflow\n\n```yaml\nstates:\n  - backlog\n  - done\ntransitions:\n  - {from: backlog, to: done, reviewer_skill: \"ghost-review\"}\n```\n"}}
		if !find(runWorkflowChecks(healthyCorpus(), stories), "unresolvable-gate", "sty_ghost") {
			t.Error("an embedded workflow naming a non-materialised reviewer must report unresolvable-gate")
		}
	})

	t.Run("class8_gate_placement_conflict", func(t *testing.T) {
		// A workflow binding debt-gate's command to a state, plus a capability
		// whose body restates running that command — the commit-push/techdebt
		// contradiction (sty_acbaa6e3).
		skills := healthyCorpus()
		skills[0].raw = "---\nname: the-workflow\nkind: workflow\napplies_to: [\"any\"]\ndescription: the lifecycle\n---\n" +
			"# wf\n\n## Checkpoint gates\n\n- [[debt-gate]] — the state's command.\n- [[exit-review]] — keeps the fixture's other gate named.\n\n```yaml\nstates:\n  - backlog\n  - {name: doing,       actor: executor}\n  - {name: debt-review, actor: satellites, command: \"run debt check\"}\n  - {name: blocked,     actor: operator}\n  - done\ntransitions:\n  - {from: backlog,     to: doing,       reviewer_skill: \"entry-review\"}\n  - {from: doing,       to: debt-review, trigger: checkpoint}\n  - {from: debt-review, on: pass, to: done}\n  - {from: debt-review, on: fail, to: doing, max_iterations: 3, on_exhausted: blocked}\n```\n"
		conflicted := append(skills, matSkill{name: "ship-capability", kind: "capability", description: "d",
			body: "Before committing run `run debt check`; on exit 0 proceed.\n",
			raw:  "---\nname: ship-capability\nkind: capability\ndescription: d\n---\nBefore committing run `run debt check`; on exit 0 proceed.\n"})
		fs := runWorkflowChecks(conflicted, nil)
		ok := false
		for _, f := range fs {
			if f.Code == "gate-placement-conflict" && f.Artifact == "ship-capability" && f.Severity == "block" {
				ok = true
			}
		}
		if !ok {
			t.Errorf("a capability restating a state-bound command must report gate-placement-conflict: %+v", fs)
		}

		// The reconciled shape: the capability references the gate by [[name]]
		// only — no execution claim, no finding.
		reconciled := append(skills, matSkill{name: "ship-capability", kind: "capability", description: "d",
			body: "Precondition: the [[debt-gate]] traverse has already passed; never restate its run.\n",
			raw:  "---\nname: ship-capability\nkind: capability\ndescription: d\n---\nPrecondition: the [[debt-gate]] traverse has already passed; never restate its run.\n"})
		for _, f := range runWorkflowChecks(reconciled, nil) {
			if f.Code == "gate-placement-conflict" {
				t.Errorf("a reference-only capability must not report gate-placement-conflict: %+v", f)
			}
		}
	})

	t.Run("class7_shallow_first_gate", func(t *testing.T) {
		skills := healthyCorpus()
		skills[1].body = "Check only that a plan exists.\n" // drops shape/acceptance/workflow/grounding
		fs := runWorkflowChecks(skills, nil)
		ok := false
		for _, f := range fs {
			if f.Code == "first-gate-shallow" && f.Artifact == "entry-review" && strings.Contains(f.Message, "grounding") {
				ok = true
			}
		}
		if !ok {
			t.Errorf("a shallow entry gate must report first-gate-shallow naming the missing layers: %+v", fs)
		}
	})
}
