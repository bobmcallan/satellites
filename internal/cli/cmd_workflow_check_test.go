package cli

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/verb"
)

// TestWorkflowCheck_InternalGateRecognised: a workflow may NAME a satellites-
// internal embedded gate (never materialised to .claude/skills); the drift
// checks must treat it as AVAILABLE — no missing-gate, no unresolvable-gate, and
// no first-gate-shallow when it is the entry reviewer (epic:satellites-backbone
// 2.4.1).
func TestWorkflowCheck_InternalGateRecognised(t *testing.T) {
	const internalGate = "satellites-selfcheck-review" // embedded in config/skills
	if !verb.IsConfigSkill(internalGate) {
		t.Fatalf("precondition: %q must be an embedded config/skills reviewer", internalGate)
	}

	// A workflow whose ENTRY reviewer and a CHECKPOINT gate are both the internal
	// gate; neither is materialised, yet the corpus must be clean.
	wfBody := "# wf\n\n## Checkpoint gates\n\n- [[" + internalGate + "]] — always.\n\n```yaml\nstates:\n  - backlog\n  - {name: doing, actor: executor}\n  - done\ntransitions:\n  - {from: backlog, to: doing, reviewer_skill: \"" + internalGate + "\"}\n  - {from: doing, to: done, reviewer_skill: \"exit-review\"}\n```\n"
	raw := "---\nname: internal-wf\nkind: workflow\napplies_to: [\"any\"]\ndescription: names an internal gate\n---\n" + wfBody
	skills := []matSkill{
		{name: "internal-wf", kind: "workflow", description: "names an internal gate", body: wfBody, raw: raw},
		{name: "exit-review", kind: "gate", description: "exit gate", body: "verify the acceptance criteria\n",
			raw: "---\nname: exit-review\nkind: gate\ndescription: exit gate\n---\nverify the acceptance criteria\n"},
	}
	story := []storyLite{{ID: "sty_x", Category: "any", Status: "backlog",
		Body: "# s\n\n## Workflow\n\n```yaml\nstates:\n  - backlog\n  - done\ntransitions:\n  - {from: backlog, to: done, reviewer_skill: \"" + internalGate + "\"}\n```\n"}}

	for _, f := range runWorkflowChecks(skills, nil, story) {
		if f.Severity != "block" {
			continue
		}
		if f.Code == "missing-gate" || f.Code == "unresolvable-gate" || f.Code == "first-gate-shallow" {
			t.Errorf("internal gate must be recognised, got %s on %q: %s", f.Code, f.Artifact, f.Message)
		}
	}

	// Control: a non-internal gate named the same way IS still missing.
	ctrlBody := "# wf\n\n```yaml\nstates:\n  - backlog\n  - done\ntransitions:\n  - {from: backlog, to: done, reviewer_skill: \"not-an-internal-gate\"}\n```\n"
	ctrl := []matSkill{{name: "ctrl-wf", kind: "workflow", description: "d", body: ctrlBody,
		raw: "---\nname: ctrl-wf\nkind: workflow\napplies_to: [\"any\"]\ndescription: d\n---\n" + ctrlBody}}
	hitMissing := false
	for _, f := range runWorkflowChecks(ctrl, nil, nil) {
		if f.Code == "missing-gate" && f.Artifact == "not-an-internal-gate" {
			hitMissing = true
		}
	}
	if !hitMissing {
		t.Error("control: a non-internal named gate must still report missing-gate")
	}
}

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
	for _, f := range runWorkflowChecks(skills, nil, nil) {
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
	for _, f := range runWorkflowChecks(healthyCorpus(), nil, stories) {
		if f.Severity == "block" {
			t.Errorf("clean corpus produced blocking finding: %+v", f)
		}
	}
}

// TestWorkflowCheck_ClientDirWorkflowGoverns: a workflow supplied as client-dir
// config (.satellites/workflows, the 2nd arg) governs a story by applies_to ↔
// category exactly as a kind:workflow skill would — no ungoverned-story, and its
// named gates (materialised in the skill set) are covered, not reported missing
// (epic:client-dir-separation order-2).
func TestWorkflowCheck_ClientDirWorkflowGoverns(t *testing.T) {
	// Skills carry ONLY the gates (no workflow); the workflow lives client-dir.
	skills := []matSkill{
		{name: "entry-review", kind: "gate", description: "entry gate",
			body: "Judge shape, plan, acceptance, the embedded workflow, and grounding.\n",
			raw:  "---\nname: entry-review\nkind: gate\ndescription: entry gate\n---\nJudge shape, plan, acceptance, the embedded workflow, and grounding.\n"},
		{name: "exit-review", kind: "gate", description: "exit gate",
			body: "verify the acceptance criteria\n",
			raw:  "---\nname: exit-review\nkind: gate\ndescription: exit gate\n---\nverify the acceptance criteria\n"},
	}
	wfBody := "# wf\n\n```yaml\nstates:\n  - backlog\n  - doing\n  - done\ntransitions:\n  - {from: backlog, to: doing, reviewer_skill: \"entry-review\"}\n  - {from: doing,   to: done,  reviewer_skill: \"exit-review\"}\n```\n"
	clientWF := []matSkill{{name: "portal-workflow", kind: "workflow", scope: "project", description: "portal lifecycle",
		body: wfBody,
		raw:  "---\nname: portal-workflow\napplies_to: [\"portal\"]\n---\n" + wfBody}}
	stories := []storyLite{{ID: "sty_portal", Category: "portal", Status: "backlog", Body: "# a portal story, no embedded workflow\n"}}

	for _, f := range runWorkflowChecks(skills, clientWF, stories) {
		if f.Severity == "block" {
			t.Errorf("a client-dir workflow governing the story must not block: %+v", f)
		}
	}
	// Without the client-dir workflow the same story is ungoverned — proving it
	// is the client-dir config that supplied governance.
	found := false
	for _, f := range runWorkflowChecks(skills, nil, stories) {
		if f.Code == "ungoverned-story" && f.Artifact == "sty_portal" {
			found = true
		}
	}
	if !found {
		t.Error("without the client-dir workflow the portal story must report ungoverned-story")
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
		if !find(runWorkflowChecks(skills, nil, nil), "unusable-skill", "entry-review") {
			t.Error("stamp above frontmatter must report unusable-skill")
		}
	})

	t.Run("class4_missing_description", func(t *testing.T) {
		skills := healthyCorpus()
		skills[2].description = ""
		if !find(runWorkflowChecks(skills, nil, nil), "unusable-skill", "exit-review") {
			t.Error("missing description must report unusable-skill")
		}
	})

	t.Run("class6_degenerate_workflow", func(t *testing.T) {
		skills := healthyCorpus()
		skills[0].raw = "---\nname: the-workflow\nkind: workflow\ndescription: d\n---\n```yaml\nstates:\n  - only\ntransitions: []\n```\n"
		if !find(runWorkflowChecks(skills, nil, nil), "workflow-lifecycle", "the-workflow") {
			t.Error("degenerate lifecycle must report workflow-lifecycle")
		}
	})

	t.Run("class1_shadow_gate", func(t *testing.T) {
		// A REPO-OWNED gate (local:true) that no workflow names is a shadow gate.
		skills := append(healthyCorpus(), matSkill{name: "lonely-gate", kind: "gate", description: "d", local: true,
			body: "one rule\n", raw: "---\nname: lonely-gate\nkind: gate\ndescription: d\n---\none rule\n"})
		if !find(runWorkflowChecks(skills, nil, nil), "orphan-gate", "lonely-gate") {
			t.Error("a repo-owned gate no workflow names must report orphan-gate (the techdebt case)")
		}
	})

	t.Run("class1_inherited_gate_is_palette", func(t *testing.T) {
		// An INHERITED gate (local:false — materialised by sync from a publisher)
		// that no workflow names is an opt-in palette, NOT drift (sty_f8f88f92).
		skills := append(healthyCorpus(), matSkill{name: "inherited-gate", kind: "gate", description: "d", local: false,
			body: "one rule\n", raw: "---\nname: inherited-gate\nkind: gate\ndescription: d\n---\none rule\n"})
		if find(runWorkflowChecks(skills, nil, nil), "orphan-gate", "inherited-gate") {
			t.Error("an inherited (palette) gate no workflow names must NOT report orphan-gate")
		}
	})

	t.Run("class5_missing_gate", func(t *testing.T) {
		skills := healthyCorpus()[:3] // drop debt-gate; the workflow's checkpoint still names it
		if !find(runWorkflowChecks(skills, nil, nil), "missing-gate", "debt-gate") {
			t.Error("a named but unmaterialised gate must report missing-gate")
		}
	})

	t.Run("class2_gate_in_capability", func(t *testing.T) {
		skills := append(healthyCorpus(), matSkill{name: "fat-capability", kind: "capability", description: "d",
			body: "Run the linter. Exit 1 (BLOCKED) → do not publish.\n"})
		fs := runWorkflowChecks(skills, nil, nil)
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
		if !find(runWorkflowChecks(skills, nil, nil), "host-coupled-system", "sys-skill") {
			t.Error("repo-dev references in a system-scope skill must report host-coupled-system")
		}
	})

	t.Run("class5_ungoverned_story", func(t *testing.T) {
		stories := []storyLite{{ID: "sty_lost", Category: "portal", Status: "backlog", Body: "# no workflow here\n"}}
		skills := healthyCorpus()
		// the-workflow applies to nothing (no applies_to in fixture frontmatter) → portal uncovered
		if !find(runWorkflowChecks(skills, nil, stories), "ungoverned-story", "sty_lost") {
			t.Error("a non-terminal story with no embedded workflow and no applies_to cover must report ungoverned-story")
		}
	})

	t.Run("class5_embedded_workflow_is_not_repo_drift", func(t *testing.T) {
		// epic:system-substrate story 5: a story's EMBEDDED ## Workflow naming a
		// non-resolvable reviewer is the story author's concern (judged at
		// engage/plan time), NOT standing repo-wide drift — so workflow check must
		// NOT emit unresolvable-gate for it.
		stories := []storyLite{{ID: "sty_ghost", Category: "x", Status: "backlog",
			Body: "## Workflow\n\n```yaml\nstates:\n  - backlog\n  - done\ntransitions:\n  - {from: backlog, to: done, reviewer_skill: \"ghost-review\"}\n```\n"}}
		if find(runWorkflowChecks(healthyCorpus(), nil, stories), "unresolvable-gate", "sty_ghost") {
			t.Error("a story's embedded ## Workflow must NOT produce repo-wide unresolvable-gate (judged at the plan gate, not workflow check)")
		}
	})

	t.Run("sty_f242eacf_server_resolved_gate_not_missing", func(t *testing.T) {
		// debt-gate is named (checkpoint) but unmaterialised; a predicate that
		// resolves it (server tier) must suppress missing-gate.
		resolvable := func(g string) bool { return g == "debt-gate" }
		skills := healthyCorpus()[:3] // drop debt-gate's materialised row
		if find(runWorkflowChecksResolved(skills, nil, nil, resolvable), "missing-gate", "debt-gate") {
			t.Error("a server-resolvable gate must not report missing-gate")
		}
	})

	t.Run("system_contained_workflow", func(t *testing.T) {
		// A scope:system workflow naming a reviewer that is NOT embed-resolvable
		// (config/skills) is not self-contained — a block. A system workflow naming
		// only embed-resolvable reviewers is clean.
		sysWFBad := matSkill{name: "sys-wf", kind: "workflow", scope: "system",
			description: "system workflow naming a non-embedded reviewer",
			raw:         "---\nname: sys-wf\nkind: workflow\nscope: system\napplies_to: [\"*\"]\n---\n## Workflow\n```yaml\nstates:\n  - backlog\n  - done\ntransitions:\n  - {from: backlog, to: done, reviewer_skill: \"not-in-the-binary\"}\n```\n",
			body:        "## Workflow\n```yaml\nstates:\n  - backlog\n  - done\ntransitions:\n  - {from: backlog, to: done, reviewer_skill: \"not-in-the-binary\"}\n```\n"}
		if !find(runWorkflowChecks([]matSkill{sysWFBad}, nil, nil), "system-not-contained", "sys-wf") {
			t.Error("a system workflow naming a non-embed-resolvable reviewer must report system-not-contained")
		}
		sysWFGood := sysWFBad
		sysWFGood.raw = strings.ReplaceAll(sysWFBad.raw, "not-in-the-binary", "satellites-intent-plan-review")
		sysWFGood.body = strings.ReplaceAll(sysWFBad.body, "not-in-the-binary", "satellites-intent-plan-review")
		if find(runWorkflowChecks([]matSkill{sysWFGood}, nil, nil), "system-not-contained", "sys-wf") {
			t.Error("a system workflow naming only embed-resolvable reviewers must be clean of system-not-contained")
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
		fs := runWorkflowChecks(conflicted, nil, nil)
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
		for _, f := range runWorkflowChecks(reconciled, nil, nil) {
			if f.Code == "gate-placement-conflict" {
				t.Errorf("a reference-only capability must not report gate-placement-conflict: %+v", f)
			}
		}
	})

	t.Run("class7_shallow_first_gate", func(t *testing.T) {
		skills := healthyCorpus()
		skills[1].body = "Check only that a plan exists.\n" // drops shape/acceptance/workflow/grounding
		fs := runWorkflowChecks(skills, nil, nil)
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
