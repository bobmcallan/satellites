# Context minimisation — satellites embedded substrate (`config/`)

**Task:** tsk_227df4a4 (epic:enforcement-surface) · **Date:** 2026-06-22 · **Re-runnable** (re-run as the embed grows).

## What this is

Analysis + proposal only. It reviews the markdown satellites ships in the client
binary embed (`config/{principles,skills,documents,workflows,mcp}`) and proposes a
distilled body per file — preserving every load-bearing rule, gate criterion, and
scope marker, cutting verbosity, restated context, narration, and historical
asides. **Applying the edits is a separate gated change (one story per file/group),
NOT this task's output.**

Token figures are estimates (`words × ~1.33`, chars/4 sanity-checked); treat them
as relative magnitudes, not exact counts. They are directional for prioritising,
which is their only job here.

## Method / what was verified mechanically

- `workflow.Parse` (`internal/workflow/workflow.go:143`) reads **only** the
  frontmatter (`name`, `applies_to`) and the **first** fenced ```yaml block
  (`states`/`transitions`). The `## Environment` section and `guardrails:` block in
  workflows are **not parsed** — pure prose.
- `guardrails:`/`ask_first`/`never` blocks are **not parsed** anywhere in non-test
  Go (`grep -rn guardrails internal/`). In both workflows and reviewer skills they
  are advisory text injected verbatim into the reviewer's `claude -p` system
  prompt. → The single largest systematic redundancy (guardrails restating the
  decision rule) is **safe to cut** as long as the decision rule itself is kept.
- `TestTaskWorkflowEmbeddedAndGatesResolve` / `TestEmbeddedGateInjectedAndUsed` pin
  specific frontmatter and gate names — those are in the **must-not-cut** list.

## Residency tiers (this is what makes a cut high- or low-value)

| Tier | When loaded | Multiplier | Files |
|---|---|---|---|
| **A — always-resident** | every session (SessionStart inject + MCP server instructions), re-anchored on each story `document_get` | **highest** — paid every session, many times | `agent-goals`, `work-artifact-selection`, `mcp/satellites_mcp_load_context` |
| **A′ — near-resident** | linked from both resident principles; fetched whenever an agent reads the model | high | `reviewer-only-model` |
| **B — per-gate reviewer** | a fresh `claude -p` each time that gate fires | high for hot gates (`intent-plan`, `story-done`, `commit-push`), low for rare ones | the 13 `skills/*-review` |
| **C — on-demand docs** | only when a task fetches them | low | `mcp_install`, `mcp_reference_*`, `surface_contract`, `system_variables`, `workflow-authoring`, `agent-operating-prompt` |
| **D — workflow** | when a story/task of that category is engaged | medium | the 3 `workflows/*` |

> **Out of scope but flagged:** the project constitution
> (`.satellites/principles/constitution.md`) is **Tier A always-resident in this
> repo** but lives in project substrate, not the `config/` embed — so it is *not*
> part of this proposal. It is a large resident cost (~600+ words injected every
> session). **Recommend a sibling task** to distil it under the same rubric.

## Before → after (config/ embed)

| Tier | Files | Before (~tok) | After (~tok) | Δ |
|---|---|---|---|---|
| A — always-resident | 3 | 1,211 | 680 | −44% |
| A′ — reviewer-only-model | 1 | 1,370 | 620 | −55% |
| B — reviewer skills | 13 | 11,153 | 7,000 | −37% |
| C — on-demand docs | 7 | 4,214 | 3,079 | −27% |
| D — workflows | 3 | 1,837 | 1,150 | −37% |
| **Total** | **27** | **~19,800** | **~12,500** | **−37%** |

**The number that matters most** — the per-session resident set (Tier A) drops
**1,211 → 680 tok (−44%)**; including the near-resident model (A+A′) it is **2,581 →
1,300 (−50%)**. Tier-A savings are paid back on *every* session; Tier-C savings are
paid back only when fetched.

---

## The systematic cut (applies to all 11 lifecycle/upsert reviewers)

Every reviewer skill repeats the same five facts 3–6× across `description` → body
intro → `## Input` → `## Decision rule` → `## Environment` → `guardrails:` →
`## Output`. None of the restatements are parsed; the LLM reads the whole body as
one prompt, so a fact stated once in the decision rule is fully in force. Cut rule:

1. **JSON output shape** (`emit exactly one {decision, notes}, no prose, no fence`)
   — stated in frontmatter description, body intro, a guardrail, *and* `## Output`.
   **Keep once** (`## Output`). Removes ~2 restatements/file.
2. **"The gate writes nothing; the client enacts your verdict"** — stated 4–6× (intro,
   Input, Decision rule, Environment, `always`, `never`). **Keep once** as a
   one-line `## Environment`.
3. **"Verify the governing workflow declares a transition `from==story_status`,
   `reviewer_skill==<self>`"** — stated in Decision rule *and* an `always` *and* a
   `never`. **Keep once** (Decision rule).
4. **"Fail closed"** — stated 2–3×. **Keep once.**
5. **`## Input` boilerplate** ("One JSON object on stdin carrying story_id,
   project_id, workspace_id, story_status, story_body") is byte-identical across all
   8 lifecycle reviewers and partly restates the reference-not-copy rule. **Shrink to
   one line** (the field list is mildly load-bearing; the surrounding prose is not).
6. **Frontmatter `description`** currently re-narrates the entire body in 4–6
   sentences. It is consumed for triggering/index only. **Cut to 1–2 sentences**
   naming what it judges + what it enacts. (Big win: these descriptions are 80–150
   words each.)

**Drop the `guardrails:` YAML block entirely** in reviewer skills where it only
restates the Decision rule + Environment (confirmed not parsed). Keep a `never`
line **only** where it carries a criterion not already in the decision rule (rare).

Net effect per reviewer: ~30–45% smaller with **zero** criterion lost — the
decision rule (the load-bearing judgment) is untouched.

---

## Per-file proposals

### Tier A — always-resident (highest value)

#### `principles/agent-goals.md` — 31 ln / ~297 tok → ~16 ln / ~150 tok (−50%)

- **Essential (keep):** status is the sole proof of done; never patch status / skip
  a gate / invent process; surface-and-stop on a blocking gap (don't work around);
  the workflow is the authority (a prescribed step is authorised, not a block); one
  story ↔ one commit ↔ one release; commit/push only at the commit-push step;
  `[[reviewer-only-model]]` link.
- **Cut:** the worked example list ("code written", "tests pass locally", "looks
  finished") → one parenthetical; the second/third explanation of why a prescribed
  step "is never a block" (stated twice); "the commitgate enforces it" aside.
- **Proposed body:**
  > Drive a story only to the terminal state of its configured workflow, every
  > reviewer gate on the path accepted. **Status is the sole proof of done** — not
  > "code written" or "looks finished".
  >
  > Never patch status to skip a gate, declare done without a reviewer accept, or
  > invent process the project didn't configure. When a gap blocks the loop (bad
  > config, missing gate skill, a human-only decision), **surface it and stop** —
  > never work around it.
  >
  > **The workflow is the authority.** Follow every transition it declares — entry
  > gate, checkpoint, and the commit-push/ship/deploy step that closes it — without
  > asking permission. A step the workflow declares is authorised by it, never a
  > "block", even when it pushes or deploys. A block is only a gap that *prevents*
  > following the workflow.
  >
  > **One story at a time:** one story ↔ one commit ↔ one release. `git commit`/`git
  > push` are authorised only at the workflow's commit-push step, never mid-work.
  >
  > See [[reviewer-only-model]].

#### `principles/work-artifact-selection.md` — 48 ln / ~600 tok → ~24 ln / ~320 tok (−47%)

- **Essential (keep):** the three primitives + their distinguishing signal
  (story=one-off→done; skill=reusable capability/dated-or-recurring output;
  task=governed re-runnable runner that records each run); recurrence is the signal;
  skill+task compose; **agent chooses the primitive, never the user**; the one
  allowed clarification is *recurrence*, never "task or story?"; config-gap-not-question
  when no governed path exists; "a task IS its work definition (ACTION/OUTPUT/VERIFICATION)".
- **Cut:** the "don't lean on the word task" sub-bullet (restates "recurrence is the
  signal"); the doubled "choosing means building it that way — and that is the
  agent's job" / "never ask a human to pick"; the elaborated parenthetical on the
  config gap.
- **Approach:** collapse the three "**bold lead** — paragraph" blocks into one
  bullet list of the primitives + a 3-line decision rule; keep the recurrence
  clarification as a single line.

#### `mcp/satellites_mcp_load_context.md` — 43 ln / ~314 tok → ~28 ln / ~210 tok (−33%)

- **Essential (keep):** "to work a story: `document_get` it → read its
  `kind:workflow` skill → drive each transition with `status_transition --skill`";
  **do not preload**; the prefix table; first-time-setup pointer; session-start
  bullets (skill sync, code index, always-context inject, work status); the on-demand
  index pointer.
- **Cut:** the parenthetical asides ("stamp-reconciled", "Grep wins for non-symbol
  text" can stay terse; "pushed, don't fetch" duplicated). Keep the *commands*, drop
  the editorial.
- **Note:** this doc is served as the MCP server instructions every session — every
  cut here is paid back on every session start. Highest unit value in the embed.

### Tier A′

#### `principles/reviewer-only-model.md` — 113 ln / ~1370 tok → ~50 ln / ~620 tok (−55%)

The most-linked principle (referenced by agent-goals, both workflows, skill-review,
workflow-review, principle-review, workflow-authoring). Big distillation, careful
to keep every load-bearing claim.

- **Essential (keep):** the role split (agent=executor, satellites=reviewer-only)
  and that it is **structural** (api-key role gate refuses an executor's status
  transition); **one enforcement primitive** = a reviewer's accept (gate/capability
  kinds retired; everything else is a non-binding guide); **WHAT not HOW**; "guides
  bind nothing"; one `claude -p` mechanism, no second copy; **authority is not yours
  to take** — running the gate ≠ taking authority, the forbidden move is *routing
  around* it; the dual failure (route-around AND decline-to-run); "process is skills,
  no release"; "status gates what is valid"; "the story is the contract".
- **Cut:** ~half the prose is the *same* point argued twice. Specifically: the
  "*Why:*" paragraph (restates "an advisory layer is not enforcement"); the
  "Pre-reading is the harness's concern" bullet (tangential to the enforcement
  contract — demote to one clause); the second restatement of "a control you could
  slip is still a line you do not cross" (already made); the "ephemeral reviewer key
  … is part of the sanctioned mechanism" elaboration (one clause suffices).
- **Approach:** keep the six `##` section headers as the skeleton; reduce each to its
  claim + one supporting sentence. No criterion is judgment — this is a guide, so
  compression risk is low (nothing here gates).

### Tier B — reviewer skills

**Worked example (the largest, most important reviewer).** Applying the systematic
cut to `satellites-task-upsert-review.md` (124 ln / ~1680 tok → ~70 ln / ~950 tok,
−43%). Every decision criterion is preserved:

> ```yaml
> ---
> name: satellites-task-upsert-review
> type: skill
> kind: reviewer
> when: status==ready||status==complete
> tags: [kind:reviewer]
> description: Task entry/definition gate. Judges a task is well-formed
>   (body declares ACTION, OUTPUT + concrete output location, and VERIFICATION)
>   and a re-runnable definition (not story-shaped), then opens it for execution
>   (ready→running, or complete→running on re-run). Emits {decision, notes}.
> ---
> ```
> Decide ONE thing: is this a **well-formed, executable, re-runnable task** ready to
> open? Entry gate of satellites-task-workflow. It does NOT judge whether the work
> is done — that is the exit gate satellites-task-report-review.
>
> **Input:** JSON on stdin — `story_id` (tsk_), `project_id`, `workspace_id`,
> `story_status`, `story_body`. The governing workflow resolves by reference (the
> `workflow:` selector or category default); an embedded `## Workflow` is display-only.
>
> **Accept** when the body declares, clearly enough for the agent to act on:
> - **ACTION** — what work to perform;
> - **OUTPUT** — the deliverable **and a concrete location** (a path/dir the run
>   writes to, e.g. `docs/reports/<name>-<date>.md`) — not merely "a report";
> - **VERIFICATION** — how success is judged (the signal the exit gate will check);
>
> and the governing workflow declares a transition `from==story_status`,
> `reviewer_skill==satellites-task-upsert-review`.
>
> **Reject** when the body is a stub / not executable work; missing any of
> ACTION / OUTPUT (incl. an OUTPUT with no concrete location) / VERIFICATION (name
> which); too vague; **story-shaped** (see below); or no matching workflow
> transition. Fail closed if it can't be read.
>
> **Structure — a task is re-runnable, not a story (priority check).** Reject a
> story-shaped body even when `type==task`, naming the defect: past-tense/one-off
> narration ("Ran X, found N"); Purpose/Approach/numbered-AC story scaffolding; or
> an embedded story workflow (`backlog→in_progress→done`). A task's contract is
> ACTION+OUTPUT+VERIFICATION read in the present/imperative. Tell the author to
> re-author (converting a story to a task is re-authoring, not a type-swap).
>
> **A `skill:<name>` tag** is the agent's Claude capability — NOTE it as a warning
> in accept notes; never resolve or reject on it.
>
> **Environment:** judge-only — read and emit a verdict; write nothing (no ledger,
> no document_upsert, no mutation). The client enacts.
>
> **Output:** exactly one JSON object, nothing else:
> `{"decision":"accept|reject","notes":"… on reject, name what is missing"}`

Cuts: the 6-sentence frontmatter description; the duplicated "no next_status / the
gate reads and judges only" (3×); the entire `guardrails:` block (restates the
above); the "Fail closed" restatement; the trailing "`decision` is accept or
reject" (already in the JSON). **Kept:** the ACTION/OUTPUT(+location)/VERIFICATION
triad, the story-shaped reject with its three tells, the skill-as-warning rule, the
workflow-transition match, fail-closed.

**Per-file numbers (same cut rule):**

| File | ln | ~tok | →~tok | Δ | Notes on what stays |
|---|---|---|---|---|---|
| `satellites-task-upsert-review` | 124 | 1680 | 950 | −43% | full triad + story-shaped tells + skill-warning |
| `satellites-intent-plan-review` | 84 | 1340 | 780 | −42% | Purpose/Approach/numbered-AC; **required `workflow:` selector, no silent default**; the `workflow list`→`embed` repair path |
| `satellites-skill-review` | 63 | 1320 | 950 | −28% | **9 rubric points kept verbatim** (esp. #8 atomic, #9 reviewer-only); cut only intro/guardrails |
| `satellites-workflow-review` | 94 | 1080 | 760 | −30% | reviewers-only + no-gate-as-code + lifecycle-soundness + `kind:gate` retired |
| `satellites-principle-review` | 80 | 905 | 620 | −31% | belief-not-procedure (reject guardrails/Spec/Verifier); residency-intentional |
| `satellites-task-report-review` | 75 | 857 | 480 | −44% | output present AND meets task's own VERIFICATION; may read ledger |
| `satellites-parent-close-review` | 70 | 795 | 480 | −40% | **keep `check:` + the children-listing semantics**; zero-non-terminal; fail-closed incl. no-children |
| `satellites-parent-cancel-review` | 71 | 720 | 410 | −43% | concrete `## Cancellation`; orthogonal-to-close (no children-terminal req) |
| `satellites-story-done-review` | 68 | 726 | 430 | −41% | numbered AC satisfied with evidence; not "looks finished" |
| `satellites-story-cancel-review` | 69 | 653 | 380 | −42% | concrete rationale (superseded/not-required) |
| `satellites-task-cancel-review` | 68 | 619 | 360 | −42% | same cancel shape |
| `satellites-selfcheck-review` | 23 | 188 | 150 | — | **do NOT cut** — load-bearing test subject; already minimal |
| `satellites-story-summary` | 22 | 270 | 250 | — | already minimal; keep model/max_tokens frontmatter |

The three cancel reviewers (task/story/parent) are **near-identical** — same Input,
same orthogonal-to-workflow rule, same `## Cancellation` superseded/not-required
check, same Environment + guardrails + Output. They differ only in noun
(task/story/anchor) and the parent variant's "does NOT require children terminal".
After the per-file cut they shrink ~42% each; a **further structural option** (not
required for this task) is a shared cancel-reviewer template — flagged, not proposed.

### Tier C — on-demand docs

| File | ln | ~tok | →~tok | Δ | Cut |
|---|---|---|---|---|---|
| `mcp/satellites_mcp_install` | 158 | 1880 | 1150 | −39% | the prose `## Fields` table **duplicates** the inline yaml comments — keep one (the table); compress the 4 intro paragraphs on auth↔match deadlock to 3 lines; keep the `validate`/`install`/`auth_bootstrap` schema verbatim (machine-read contract) |
| `documents/workflow-authoring` | 74 | 540 | 400 | −26% | keep the type+kind table, the two frontmatter templates, the live-examples commands; cut restated "drafting is ungated, gate is at upload" (3×) |
| `documents/satellites_surface_contract` | 55 | 531 | 360 | −32% | keep MCP-vs-client split + "refinement judged at first gate" + "writes routed by type, gated iff reviewer exists"; cut the long headline (restates the body) and "direction of travel" aside |
| `documents/system_variables` | 38 | 406 | 380 | −6% | **mostly keep** — the variable table is a contract; cut only the duplicated resolution-order prose |
| `mcp/satellites_mcp_reference_documents` | 44 | 410 | 370 | −10% | already lean; trim restated "stories and documents are one substrate kind" |
| `mcp/satellites_mcp_reference_dispatch` | 34 | 298 | 270 | −9% | already lean |
| `documents/agent-operating-prompt` | 7 | 149 | 149 | — | **do NOT cut** — single tight paragraph, every clause load-bearing |

### Tier D — workflows

| File | ln | ~tok | →~tok | Δ | Cut |
|---|---|---|---|---|---|
| `satellites-baseline-workflow` | 74 | 753 | 430 | −43% | **drop the entire `## Environment` + `guardrails:` block** (not parsed; restates the gates); compress the 3 over-long prose paragraphs to the bullet list already present. **Keep the fenced `## Workflow` yaml verbatim.** |
| `satellites-task-workflow` | 59 | 690 | 430 | −38% | keep re-runnable/episode rule + the driving loop + the yaml; cut the doubled "the reviewer is the single enforcement primitive" and authoring-vs-driving overlap |
| `satellites-parent-workflow` | 54 | 394 | 290 | −26% | drop `## Environment`+guardrails; keep the 2-step close/abandon instructions + yaml |

> **Workflow safety rule:** the first fenced ```yaml block (`states`/`transitions`)
> and the frontmatter `name`/`applies_to`/`kind` are parsed — **never touch them**.
> Everything else in a workflow file is prose the engine ignores and is freely
> compressible.

---

## Must-NOT-cut (load-bearing — dropping these breaks the binary or a gate)

1. **Frontmatter contract fields**, parsed by the binary/resolver:
   `name`, `type`, `kind`, `scope`, `when`, `applies_to`, `check`, `tags`, and for
   `story-summary` `model`/`max_tokens`/`enabled`. (`workflow.Parse` requires
   `name`+`applies_to`; `verb.IsConfigSkill` keys on the `satellites-*-review`
   names.)
2. **Workflow state machines** — the first fenced ```yaml block in every
   `workflows/*` (and the embedded one in any story). `extractYAMLBlock` parses it.
3. **`check:` shell commands** in `satellites-parent-close-review` (`satellites
   story children …`) and `satellites-selfcheck-review` (`test -f go.mod`) — the
   harness runs them.
4. **`satellites-selfcheck-review.md`** as a whole — it is the load-bearing subject
   of `TestEmbeddedGateInjectedAndUsed` (proof that an embed reviewer absent from
   `.claude/skills/` is resolved + injected + used). Already minimal; leave it.
5. **`agent-operating-prompt.md`** — already a single tight paragraph.
6. **Every reviewer's decision criteria** — compress wording, never drop a
   criterion. Named explicitly: task-upsert's ACTION/OUTPUT(+location)/VERIFICATION
   triad and the three story-shaped tells; intent-plan's required-`workflow:`-selector
   (no silent default); skill-review's 9 points (esp. #8 atomic, #9 reviewer-only);
   workflow-review's reviewers-only/no-gate-as-code/lifecycle-soundness;
   principle-review's belief-not-procedure + residency-intentional; the cancel
   reviewers' concrete-rationale + already-terminal rejects; parent-close's
   zero-non-terminal-children + fail-closed-on-no-children.
7. **The `{decision, notes}` JSON output shape** — stated once per reviewer (the
   harness parses it).
8. **The gate-names** `satellites-task-upsert-review`, `satellites-task-report-review`,
   `satellites-task-cancel-review`, and the `satellites-*-review` family — pinned by
   tests and named on workflow edges.

---

## Prioritised cut list (do these in order; one story per row)

| # | Target | Tier | ~tok saved | Why first |
|---|---|---|---|---|
| 1 | `mcp/satellites_mcp_load_context` | A | ~100 | served every session as MCP instructions — highest unit value |
| 2 | `principles/agent-goals` + `work-artifact-selection` | A | ~430 | resident every session; small, safe, high-multiplier |
| 3 | `principles/reviewer-only-model` | A′ | ~750 | biggest single guide; most-linked; pure prose (no gate risk) |
| 4 | Reviewer-skill systematic cut — the 11 lifecycle/upsert reviewers (one story, mechanical, shared rule) | B | ~3,800 | one rule applied 11× = the largest total saving; zero criterion lost |
| 5 | `mcp/satellites_mcp_install` (de-dupe table vs yaml comments) | C | ~730 | single biggest file; on-demand so lower multiplier |
| 6 | Workflows — drop unparsed `## Environment`/guardrails | D | ~690 | safe (engine ignores them) |
| 7 | Remaining Tier-C docs (`surface_contract`, `workflow-authoring`, `system_variables`, reference docs) | C | ~280 | lean already; lowest priority |
| — | **(sibling task)** distil project `constitution.md` | A (project) | ~300+ | Tier-A resident but outside `config/`; own task |

Group 4 is the single highest-leverage change: a mechanical, criterion-preserving
pass over the reviewer family driven by one shared rule.

---

## Verification — every reviewed doc's essential contract is preserved

Spot-checks that each minimised reviewer would render the **same verdict** as today:

- **task-upsert-review** — minimised body keeps ACTION + OUTPUT-with-concrete-location
  + VERIFICATION and the three story-shaped tells (past-tense / Purpose-Approach-AC /
  embedded story workflow). A story-shaped body or a missing output location still
  rejects; this very task (tsk_227df4a4) still accepts. ✓
- **task-report-review** — keeps "output present AND meets the task's own declared
  VERIFICATION" + the ledger-read allowance. A placeholder/partial report still
  rejects. ✓
- **intent-plan-review** — keeps Purpose/Approach/numbered-AC + the **required
  `workflow:` selector (no silent default)** + the `workflow list`→`embed` repair.
  A story with no recorded selector still rejects. ✓
- **story-done-review** — keeps "numbered AC satisfied with evidence, not 'looks
  finished'". ✓
- **skill-review** — all 9 rubric points kept verbatim; #8 (atomic) and #9
  (reviewer-only-no-enact) untouched, so an enacting reviewer still rejects. ✓
- **workflow-review** — keeps reviewers-only enforcement, no-gate-as-code, lifecycle
  soundness, retired-`kind:gate`. A baked-process edge still rejects. ✓
- **principle-review** — keeps belief-not-procedure (rejects guardrails/Spec/Verifier)
  and residency-intentional flagging. A skill-shaped doc still rejects. ✓
- **parent-close-review** — `check:` and children-terminal semantics kept; a
  non-terminal child still rejects; fail-closed on unreadable check / no children
  kept. ✓
- **cancel reviewers (×3)** — concrete-`## Cancellation` rationale + already-terminal
  reject kept; parent's "children need not be terminal" kept. ✓
- **selfcheck-review / agent-operating-prompt / story-summary** — not cut. ✓
- **workflows** — only unparsed prose removed; the parsed state machines and
  frontmatter are byte-identical, so `workflow.Parse` and the pinning tests are
  unaffected. ✓

**Resident-context reduction quantified:** Tier A 1,211 → 680 tok (−44%); A+A′ 2,581
→ 1,300 (−50%); full `config/` embed ~19,800 → ~12,500 (−37%).

**Explicitly must-NOT-cut:** `satellites-selfcheck-review.md`,
`agent-operating-prompt.md`, all frontmatter contract fields, all workflow state
machines, all `check:` commands, and every reviewer decision criterion (see list
above). The project `constitution.md` is resident but **out of scope** for this
embed pass — recommend a sibling task.

## Outcome

A concrete, safe, prioritised minimisation plan a follow-up story can apply
file-by-file: 7 ordered cut-stories (+1 sibling task for the constitution), ~7,300
tokens removed from the embed (~531 of them from the per-session resident set),
with a verified guarantee that no rule, gate criterion, or scope marker is dropped.
