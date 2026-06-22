# Repo-agnostic audit (re-run) — `config/` + `internal/`

**Task:** tsk_106ee5ff (epic:enforcement-surface) · **Date:** 2026-06-23 · **Re-run** of the 2026-06-22 baseline.

Re-runs the repo-agnostic audit after the `epic:enforcement-surface` remediation
landed (F1, F2, F3, F5 + the lifecycle-config primitives). Method unchanged: three
**independent** sweeps (`internal/` literals, `internal/` baked decisions, `config/`
overreach), unbiased (not seeded with the prior findings), reconciled here against
the **14 TRUE-violations** the first run recorded
(`docs/reports/repo-agnostic-audit-2026-06-22.md`).

## Headline

- **The original list shrank to ZERO un-addressed.** All 14 prior TRUE-violations
  are remediated (13 fixed and shipped; 1 deferred with an in-code rationale).
- **The deeper re-scan surfaced a NEW layer** the first pass missed — **7 new
  TRUE-violations**, dominated by a **CI-chain bake family** (`test`/`release`/
  `deploy` and the `deploy`-stage anomaly rule) plus two `workflow check`
  checklists, the task-lifecycle shape pin, and a stale `config/` authoring doc.
- This is the audit working as a **living control**: confirmed remediation +
  found the next layer. Net TRUE-violation count: **14 → 7** (all 7 new), with the
  original families closed.

## Before → after: the original 14

| # / family (2026-06-22) | Site | Now | How |
|---|---|---|---|
| **F1** commit-push ship-step bake | `workflow.go IsCommitStep`, `commitgate:148`, `cmd_work resolveEngageGuards` | ✅ **fixed** | sty_028c3f92 — `IsCommitStep` + the `"commit-push"` literal removed; commitgate requires only an editable engagement (grep: no gating literal remains; sweep confirms the revert) |
| **F2** kind→gate selector | `cmd_validate_artifact validateGateForKind` | ✅ **fixed** | sty_8e9d409b — `verb.EntryReviewer` resolves from the workflow; the kind switch is a fallback only (sweep cleared it as mechanism) |
| **F3·3a–3d** audit status literals (×4) | `processtrace/audit.go` `shipStatuses`/`done`/`in_progress` | ✅ **fixed** | sty_9f97ff5c — `StoryAudit.Terminal/Editable` workflow classifiers; detectors use them |
| **F3·3f,3g** epic guards | `verb/document.go epicChildRefusal/epicReparentRefusal` | ✅ **fixed** (status names) | sty_781c96aa — `IsTerminalForCategory`/`IsInitialForCategory` (server-side `SystemWorkflowSources`). *See N6 — the freeze RULE is a new, separate finding.* |
| **F3·3h** terminal cleanup | `cmd_story_review:748` | ✅ **fixed** | `verb.IsTerminalForCategory` |
| **F3·3i** ReachedTerminal | `cmd_evidence_review:125` | ✅ **fixed** | `verb.IsTerminalForCategory` |
| **F3·3j** view filter | `server/story_filter isTerminalStatus` | ✅ **by-design** | cross-type stories+tasks view; reclassified, unchanged |
| **F3·3k** create default | `document/store` default status | ✅ **by-design** | runs before a workflow selector exists; reclassified |
| **F3·3e** `startedStatus` | `cmd_context_repair:123` | ⏸ **deferred (documented)** | in-code NOTE (sty_9f97ff5c): needs full-source resolution of repo-specific WORKING states; breaks the pure planner's testability. Both sweeps recognised the deferral. |
| **F4** category override | `cmd_story_setstatus setStatusAllowed` | ✅ **fixed** | sty_781c96aa — `category=="parent"` removed; `GoverningUngatedAdvance` honours the parent workflow's `trigger:reopen` edge (grep: gone) |
| **F5** resident principle | `config/principles/agent-goals.md` | ✅ **fixed** | sty_3375eeda — distilled to repo-agnostic core (grep: 0 commit-push/ship/deploy/one-commit); now passes its own `principle-review` scope-true bar |

**Original-list reduction: 14 → 0 un-addressed** (13 fixed, 1 deferred-with-rationale).
Two additional lifecycle gaps surfaced *during* remediation were also closed —
the workflow had no cancel path (sty_d25f5577) and `checkpointDecision` rejected a
gated edge from a checkpoint state (sty_57adfd04).

---

## New TRUE-violations (the next layer)

The unbiased re-scan went deeper than the first pass and found a **CI-chain bake**
the original audit missed entirely, plus several `workflow check` opinions.

### N1 — the CI-chain is baked into the binary (the dominant new family)

| # | Site | What's baked | Clause |
|---|---|---|---|
| **N1a** | `internal/cli/cmd_evidence_fromhead.go:23` | `ciStages = []string{"test","release","deploy"}` — *this* repo's GitHub-Actions chain hardcoded as both workflow names AND evidence labels | mechanism-not-behaviour |
| **N1b** | `internal/cli/cmd_evidence.go:34` | `validCIStages = {test,release,deploy}` — `evidence ci` rejects any stage outside this closed set (no config knob) | mechanism-not-behaviour |
| **N1c** | `internal/processtrace/audit.go:253` | `Stage == "deploy"` drives the `ungated-deploy` ERROR — a process rule keyed off one CI-stage NAME. A repo whose final stage is `ship`/`publish`/`promote` gets no enforcement | no-gate-as-code |

> **Note on N1c — a reclassification.** The first remediation (sty_9f97ff5c)
> *deliberately kept* `Stage == "deploy"` as a "CI-stage fact, by-design." The
> re-audit (independent) argues the CI-stage NAME is itself this repo's shape, so
> the "deploy ⇒ story must be terminal" rule is baked process. This is a genuine
> judgment revision — N1c belongs with N1a/N1b as the CI-chain-bake family, not a
> by-design carve-out. The commitgate ship-step bake was removed (F1); **the same
> class survives in `evidence` + `processtrace`.**

### N2 — `workflow check` bakes reviewer-content + terminal opinions

| # | Site | What's baked | Clause |
|---|---|---|---|
| **N2a** | `internal/cli/cmd_workflow_check.go:366` `firstGateLayers` | `{shape,plan,acceptance,workflow,grounding}` — BLOCKS any workflow whose entry reviewer body doesn't mention all five literal strings. An opinion about what a reviewer must cover, unconfigurable | no-gate-as-code |
| **N2b** | `internal/cli/cmd_workflow_check.go:292` + `cmd_context_validate.go:233` `terminalStoryStatuses` | `{done,cancelled,deleted}` — a hardcoded terminal set with **no workflow fallback** (unlike `IsTerminalForCategory`). A repo whose terminal is `shipped`/`closed` gets finished stories flagged `ungoverned`. *Same Family-3 class the first pass missed.* | determinism-is-not-a-licence |

### N3 — task-lifecycle shape pinned in Go

| # | Site | What's baked | Clause |
|---|---|---|---|
| **N3** | `internal/cli/cmd_validate_taskshadow.go:61` `taskShapedWorkflow` | requires any `applies_to:[task]` workflow to declare states `ready`/`running`/`complete`; blocks `todo→doing→done`. The canonical task lifecycle's state names baked in | mechanism-not-behaviour |

### N4 — epic lifecycle RULE (not just the status names) is baked

| # | Site | What's baked | Clause |
|---|---|---|---|
| **N4** | `internal/verb/document.go:1360,1389` | the status CLASSIFICATION is now engine-derived (F3 fixed that), but the RULE — "epic membership frozen once started", "no children on a terminal epic" — is a Go branch no workflow can opt out of. Lower confidence (defensible as referential integrity) | mechanism-not-behaviour |

### N5 — stale shipped authoring doc contradicts the reviewer-only model

| # | Site | What's wrong | Clause |
|---|---|---|---|
| **N5** | `config/documents/workflow-authoring.md:37` | the reviewer-skill template tells authors the body must describe "the ledger rows it appends to ENACT the transition on accept" — i.e. teaches authors to write an ENACTING reviewer, the exact defect the shipped `satellites-skill-review` rule 9 blocks and every shipped gate disclaims ("the gate writes NOTHING; the client enacts") | mechanism-not-behaviour (stale substrate) |

---

## Confirmed repo-agnostic (cleared — unchanged from the first run + re-verified)

- **The hook doors & engine** — START door, commitgate (ship-step bake **reverted**,
  comment cites sty_028c3f92), Stop goal-keeper, v2 dispatcher, gate run path,
  `IsEditable`/`IsTerminal`/`InitialState` (shape-derived), `setStatusAllowed`
  (defers to `GoverningUngatedAdvance`), surface gate.
- **The `…ForCategory` helpers** (`workflow_resolve.go`) — workflow-first with a
  canonical literal fallback only when nothing resolves; mechanism.
- **Sanctioned naming** — `satellites-` prefix derivation (`localSkillName`,
  `reviewSkillForKind`), the constitution-named `satellites-story-summary` hook
  (a sweep mis-flagged it; overridden to legitimate), `IsReviewer` kind classifier.
- **Engine/protocol vocabulary** — ledger kinds, edge outcomes (`pass`/`fail`/
  `checkpoint`/`reopen`), typed `Scope`/`Type`/`Transport`, id prefixes, actor
  roles. Data model, not substrate process names.

---

## Prioritised remediation for the new layer (one story per group)

| Pri | Group | Fix |
|---|---|---|
| 1 | **N1** CI-chain bake | make the CI chain configuration (a TOML/`config` list of stage names) that `evidence ci`, `cmd_evidence_fromhead`, and `audit.go`'s ungated-deploy rule read — drop the `{test,release,deploy}` / `"deploy"` literals. Highest-leverage: it's a whole repo's CI shape in the binary. |
| 2 | **N2b** `terminalStoryStatuses` | route through `IsTerminalForCategory` (the sanctioned fallback pattern) — both `workflow_check.go:292` and `context_validate.go:233`. Drop-in. |
| 3 | **N2a** `firstGateLayers` | make the required first-gate layers configuration (or drop the substring checklist) — a reviewer's rubric is substrate, not a binary checklist. |
| 4 | **N3** `taskShapedWorkflow` | derive task-lifecycle soundness structurally (reachable terminal, gate resolvability) instead of pinning `ready/running/complete`. |
| 5 | **N5** `workflow-authoring.md` | one-line edit: "the ledger rows it appends to ENACT…" → "the decision rule; the gate writes NOTHING — the client enacts on accept." Cheap, removes a self-contradiction. |
| 6 | **N4** epic freeze RULE | (lower confidence) make the freeze-on-start / no-child-on-terminal policy a reviewer verdict or workflow-declared, not a Go refusal. |
| — | **F3·3e** `startedStatus` | still deferred — bundle with the N-work when full-source working-state resolution is built. |

---

## Verification

- Every finding cites a concrete `file:line` and a named constitution clause;
  each classified TRUE-violation vs sanctioned-mechanism with a one-line
  justification (a sweep's `satellites-story-summary` false-positive was
  re-verified and overridden to legitimate). ✓
- The known `commit-push` literal is **confirmed removed** (grep: no
  `IsCommitStep`/`== "commit-push"` gating literal), and the original Family-1/2/4/5
  sites are confirmed remediated by direct grep + the sweeps. ✓
- **Original-list reduction quantified:** 14 → 0 un-addressed (13 fixed + 1
  deferred-with-rationale). **Net TRUE count 14 → 7** (the 7 new deeper findings). ✓
- Confirmed-repo-agnostic surfaces re-enumerated so the next re-run skips
  re-litigating sanctioned mechanism. ✓

**Outcome:** the remediation is verified — the original repo-agnostic violations
are closed — and the audit, run again, has surfaced the next, deeper layer (the
CI-chain bake + workflow-check opinions). A clean, prioritised 6-story plan for
that layer; re-running after it lands should again shrink the list.
