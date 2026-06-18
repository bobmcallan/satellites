# Constitution audit — process baked into the binary

**Trigger:** A review of `.satellites/workflows/satellites-workflow.md` surfaced
that a prior "reset to a gateless baseline" stopped at the substrate and never
touched the binary, because the binary self-justifies its embedded process as
"mechanism". This audit enumerates **every site where process / gate / opinion
is baked into the Go binary**, against the resident constitution
(`.satellites/principles/constitution.md`):

> It ships no process of its own as code… **No gate as code.** The binary RUNS
> gates; it never IS one. A version-bump rule, a debt rule, or a surface rule
> baked into the binary is the defect this constitution exists to prevent — no
> other repo could change it.

Remediation is tracked by `epic:minimal-spine` (anchor `sty_632830c9`). Items
already landed are marked ✅.

## Target architecture (operator direction)

A **default / blank** satellites install gives a story-driven loop with the
smallest possible internal spine:

1. Default workflow `backlog → in_progress → done` (one non-terminal working
   state; ungated exit + cancel).
2. The internal spine ensures only: the **story is satellites-formatted**, the
   **agent's goal is story → done**, and the **agent follows the story**. It does
   NOT enforce config-over-code, techdebt, or integration — those are an opt-in
   substrate palette a repo composes.
3. **Invariant:** every gate injected into `claude -p` resolves to exactly one
   home — required in `.claude/skills/` (editable substrate) OR a sanctioned
   embedded allowlist (the reduced spine). No divergent shadow; no embedded gate
   outside the allowlist. An embedded gate not present in `.claude/skills/` must
   actually be injected and USED by the agent (proven, not assumed).

## 1. Embedded gates — `internal/verb/internalgates/`

`internal/verb/internal_gates.go:21` — `//go:embed internalgates/*.md`. Resolved
by `internalGateRaw` / `resolveGateSkill` BEFORE the worktree, so the embedded
copy wins and is never materialised to `.claude/skills/`.

| File | Disposition |
|---|---|
| `satellites-intent-plan-review.md` | KEEP-REDUCE — strip config-over-code; reduce to story-format + goal (epic order-5). |
| `satellites-internal-selfcheck.md` | KEEP-REDUCE — repurpose to the agent-follows-story spine member (epic order-5). |
| `satellites-intent-code-review.md` | ✅ REMOVED from the embed; re-homed in `.satellites/skills/` (epic order-3). |

An embedded `kind:gate` markdown with a judgment prompt is the binary *being* a
gate — the core break. The `internal_gates.go` "satellites' OWN governance…
mechanism only" comment is the loophole the reset stopped at.

## 2. Baseline workflow as a Go constant — `cmd_init.go`

`cmd_init.go:416` `const baselineWorkflowDoc` wires `satellites-intent-plan-review`
as the entry gate and names `satellites-intent-code-review`. **REWIRE** to the
reduced gate; drop the intent-code checkpoint and config-over-code prose (epic
order-6).

## 3. Hardcoded state-name opinions — DERIVE-FROM-ENGINE

`internal/workflow/workflow.go` already derives state semantics structurally
(`InitialState`, `IsTerminal`, `TerminalStates`, `IsEditable`). Every site below
re-decides those from a baked name-list instead of asking the engine — so a repo
that renames its states is silently mishandled (epic order-7).

| File:line | Hardcoded | Engine method |
|---|---|---|
| `cli/cmd_work.go:355` | `terminalPhases = {done, cancelled, canceled}` | `IsTerminal` |
| `cli/cmd_hook_stopcheck.go` | `isTerminalPhase` (goal-keeper release) | `IsTerminal` |
| `cli/cmd_story_review.go:512` | `observed == "done"` | `IsTerminal` |
| `cli/cmd_workflow_check.go:282` | `terminalStoryStatuses = {done, cancelled, deleted}` | `TerminalStates` |
| `cli/cmd_context_repair.go:116` | `{in_progress, techdebt-review, integration-review, done-review, blocked}` | `!= InitialState()` |
| `cli/cmd_evidence_review.go:125` | `done`, `cancelled` | `IsTerminal` |
| `document/store.go:200` | default `"backlog"` | `InitialState()` |
| `verb/document.go:1157` | `{backlog, ready}` | `InitialState`/`IsEditable` |
| `verb/document.go:1188` | `{done, cancelled}` | `IsTerminal` |
| `server/story_filter.go:114,123` | `{done, cancelled}` | `IsTerminal` |
| `server/project_detail.go:299` | `{backlog, done, cancelled, canceled, ""}` | `IsEditable` |
| `processtrace/audit.go:46,201,228,255` | `{done, deploy}`, `done`, `{in_progress, done}` | `IsTerminal`/`IsEditable` |

Stray `deleted` / `deploy` / `canceled` names are tells the code is *guessing*
because it refuses to read the workflow.

## 4. Dead / divergent substrate shadows — RESOLVE-FORK

The intent gates existed in BOTH `internalgates/` (binary) and `.satellites/skills/`
(substrate), divergent, binary winning silently. `intent-code-review` is now
single-homed (order-3); `intent-plan-review` to be resolved when reduced (order-5).
The home-of-gate control (order-8) enforces one-home-per-gate.

## 5. Satellites-internal admin substrate — SEPARATE (lower priority)

`config/documents/embed.go:30` seeds review/authoring/design skills into the
server DB at boot — server-side substrate seeding, adjacent to mechanism. Triage
as a separate workstream (epic order-8 region).

## 6. Controls gap — why the reset failed

- `satellites workflow check` is TAUGHT to bless embedded gates (`IsInternalGate`
  → "treat as AVAILABLE"), so it can never flag baked process.
- `satellites-intent-code-review` judges story diffs only, never the binary's own
  embedded gates.
- No control asserts the §-invariant (one home per injected gate; no divergent
  shadow; embedded only from the allowlist). The home-of-gate validation control
  (epic order-8) closes this, and must be dogfooded.

## 7. CLEAN (confirmed mechanism — no action)

The workflow engine, the review-dispatch path (`request_review_dispatcher.go`,
`v2_edges.go`, `workflow_resolve.go`), the goal-keeper Stop hook, and the
commit-gate hook are correct generic mechanism — the model the rest should follow.
