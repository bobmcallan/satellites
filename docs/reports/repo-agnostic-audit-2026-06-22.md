# Repo-agnostic audit — `config/` + `internal/`

**Task:** tsk_106ee5ff (epic:enforcement-surface) · **Date:** 2026-06-22 · **Re-runnable.**

Audits the satellites binary (`internal/`) and shipped client substrate (`config/`)
for **constitution violations**: BEHAVIOUR held in the binary instead of MECHANISM,
process DECISIONS baked as Go branches, hardcoded substrate NAMES, and shipped
config that assumes a workflow/repo shape. Judged against
`.satellites/principles/constitution.md`.

## Headline

- **14 TRUE-VIOLATIONS** found, in **4 families** (the `commit-push` ship-step bake,
  a kind→gate selector, a large literal-status-vocabulary family, and a
  category-based ungated override) **+ 1 shipped-principle overreach**.
- The known `commit-push` violation was **detected by independent search** (2 of 3
  sweeps flagged it without being told it existed), so the **search context is
  adequate — no widening needed**. It in fact surfaced a whole class the seed only
  hinted at.
- A fix story for the `commit-push` violation already exists: **sty_028c3f92**.
- **No baked version-bump, changelog-required, or debt-threshold gate exists** in Go
  (confirmed) — those surfaces are clean.

## Method

Three **independent** sweeps, none told about the seeded `commit-push` finding (so
detection is a real test, not an echo):

1. `internal/` — hardcoded substrate-name string literals driving behaviour.
2. `internal/` — enforcement/process decisions baked as Go branches.
3. `config/` — shipped baseline over-reach + baseline workflow shape inventory.

Findings reconciled here. **Classification discipline:** the constitution's own
carve-outs are honoured — *reading* a configured field is mechanism; *branching on
its specific VALUE* is behaviour. A genuine engine/protocol token (typed `Scope`/
`Type`, `pass`/`fail` edge outcomes, the `satellites-` skill prefix, the
constitution-named summary reviewer) is mechanism, not a substrate process name.

> **False-positive lesson (recorded per the task's verification bar):** a parallel
> Explore sub-sweep mass-flagged ~20 typed-enum branches (`Scope`, `Type`,
> `Transport`, roles, edge outcomes, the `kind:reviewer` classifier) as violations.
> Every one was re-verified against the data model and the constitution's explicit
> carve-outs and **discarded** — flagging sanctioned mechanism is itself the defect
> the task warns against. The list below is the verified set.

---

## Family 1 — the `commit-push` ship-step bake (the seeded finding) — TRUE

**Sites:**
- `internal/workflow/workflow.go:451` — `IsCommitStep`: `if strings.TrimSpace(t.WorkSkill) == "commit-push"`.
- `internal/cli/cmd_hook_commitgate.go:148` — denies `git push`/`commit` unless `eng.CommitReady`.
- `internal/cli/cmd_work.go` (`resolveEngageGuards`) — `commitReady` fails **closed** to `false` for any state that is not a resolved commit-push step.
- **Disagrees with** `config/workflows/satellites-baseline-workflow.md` — the shipped default (`applies_to:["*"]`) declares **no `work_skill: commit-push` edge** (states: `backlog`, `in_progress`, `done`, `cancelled`; every edge is a `reviewer_skill` gate; no checkpoint/ship step).

**What's baked:** the binary authorises commit/push only at the state whose
`work_skill` literally equals `"commit-push"`, and **fails closed otherwise**. That
makes the "one story ↔ one commit ↔ one release / push only at the ship step"
*opinion* mandatory for every repo.

**Why it breaks repo-agnostic operation (concrete):** a repo on the shipped minimal
baseline engages a story, edits (allowed), then `git commit`/`git push` → denied
with "checkpoint to the ship step (`work_skill: commit-push`)" — **a state that
does not exist in the baseline.** The default workflow can reach `done` but can
**never commit or push, in any repo.** Observed in `base-install-probe`.

**Constitution clause:** *No gate as code* / *mechanism-not-behaviour* — "BEHAVIOUR…
the process it enforces lives in the substrate"; *determinism is never a licence to
hardcode the DECISION.*

**Classification dissent (adjudicated):** sweep 2 **cleared** this as "mechanism"
(the binary reads a config field; a repo can omit the edge). That reading
under-weights the **fail-closed default**: omitting the edge **blocks** push, it
doesn't free it — so the opinion is imposed, not opted into, and the shipped
baseline is bricked. Sweeps 1 and 3 classified it TRUE; the project owner has ruled
it an overreach. **Verdict: TRUE-VIOLATION.**

**Remediation:** the commitgate should enforce only the workflow-agnostic rule (a
mutation/push requires a lease-fresh editable engagement) and drop the
`commit-push`-literal push binding. One-story↔one-commit enforcement, if still
wanted, belongs in a reviewer (substrate), not a Go branch — and **not** by adding a
ship step to the minimal baseline. → **sty_028c3f92** (created).

---

## Family 2 — kind→gate selector baked in Go — TRUE

- `internal/cli/cmd_validate_artifact.go:66-71` — `validateGateForKind` returns the
  reviewer name (`"satellites-intent-plan-review"` / `"satellites-task-upsert-review"`)
  from a literal `switch` on doc kind, then dispatches it (`:97`).

**What's baked:** *which gate runs* — explicitly substrate's domain ("which gate
runs lives in the substrate") — is a hardcoded literal map in the binary.
**Clause:** mechanism-not-behaviour. **Remediation:** resolve the entry reviewer
from the governing workflow's entry edge, not a Go switch.

---

## Family 3 — literal status/state vocabulary instead of engine-derived lifecycle — TRUE (family)

The binary branches on **specific status/state name literals** to make lifecycle
decisions, when the engine already exposes the shape-derived answer
(`workflow.IsTerminal` / `InitialState` / `TerminalStates` / `IsEditable`). Any repo
that uses a minimal workflow or renames a state silently misfires.

| # | Site | Literal decision |
|---|---|---|
| 3a | `internal/processtrace/audit.go:46` (used :142) | `shipStatuses = {"done","deploy"}` |
| 3b | `internal/processtrace/audit.go:228` | `Stage=="deploy" && CurrentStatus!="done"` (ungated-deploy audit) |
| 3c | `internal/processtrace/audit.go:201` | `CurrentStatus=="done"` (reject-no-follow-up) |
| 3d | `internal/processtrace/audit.go:255` | `!="in_progress" && !="done"` (unengaged-work) |
| 3e | `internal/cli/cmd_context_repair.go:114-118` | `startedStatus`: `{in_progress, techdebt-review, integration-review, done-review, blocked}` |
| 3f | `internal/verb/document.go:1391-1397` | `epicChildRefusal`: `{done, cancelled}` |
| 3g | `internal/verb/document.go:1364-1368` | `epicReparentRefusal`: `{backlog, ready}` |
| 3h | `internal/cli/cmd_story_review.go:748` | `observed=="done"` (comment **admits** the bake: "client holds no workflow knowledge, so it keys on the canonical terminal status the fix/feature/parent/urgent workflows share") |
| 3i | `internal/cli/cmd_evidence_review.go:125` | `=="done" \|\| =="cancelled"` (ReachedTerminal) |
| 3j | `internal/server/story_filter.go:106-110` | `isTerminalStatus`: `{done, cancelled, canceled, inactive}` |
| 3k | `internal/document/store.go:200,203,206` | create default `Status="backlog"` (lower severity — overridable default; should derive `InitialState()`) |

**Why it breaks repo-agnostic operation:** a repo whose workflow has no `deploy`
stage, no `*-review` states, or that renames `in_progress`→`active` gets: process
audits that never fire (3a-3e), epic-membership rules that mis-classify (3f-3g),
terminal cleanup/outcome/list-filtering that misjudge "done" (3h-3k). The
constitution's own example minimal `backlog→in_progress→done` repo misfires on
several. **Clause:** mechanism-not-behaviour / determinism-is-not-a-licence.
**Remediation (one story):** route every site through the existing engine
derivation (`IsTerminal`/`InitialState`/`TerminalStates`/`IsEditable`) — the pattern
the rest of the codebase already uses.

---

## Family 4 — category-based ungated override — TRUE

- `internal/cli/cmd_story_setstatus.go:69,94,102` — `setStatusAllowed`/`requireParent`
  grant an **un-gated operator status change** when the configured `category` field
  literally equals `"parent"`.

**What's baked:** a process decision (a parent may bypass the reviewer gate) keyed
off a substrate category NAME. **Clause:** *no-gate-as-code* / substrate-naming.
**Remediation:** drive the override authority from workflow/config, not a literal
category check.

---

## Family 5 — shipped resident principle bakes a release lifecycle — TRUE

- `config/principles/agent-goals.md:19-29` (`scope:system`, **`principles:always`** —
  injected into **every session of every adopter**) asserts the governing workflow
  has "the entry gate, the checkpoint, and the **commit-push / ship / deploy step
  that closes it**" and "one story ↔ one commit ↔ one release … the commitgate
  enforces it."

**What's baked:** satellites ships its *own* dev/release process opinion as resident
guidance to every repo. The shipped minimal baseline has no checkpoint, no
commit-push step, no ship/deploy step — so a minimal repo is told, every session,
to follow a process it isn't configured for, and is pointed at the Family-1
enforcement that blocks it. **Cross-check:** this principle would **fail its own
sibling gate** — `config/skills/satellites-principle-review.md` rule 4 rejects a
system principle that "leans on repo-dev specifics." **Clause:** "ships no process
of its own as code." **Remediation:** distil `agent-goals` to the repo-agnostic
core (drive to the workflow's terminal state through its gates; status is the sole
proof of done) and move the commit-push/ship/deploy specifics to *this* repo's
project substrate. (Dovetails with the resident-context minimisation task
tsk_227df4a4.)

---

## Borderline — defensible mechanism, flagged for a deliberate decision

- **Engine actor vocabulary** `satellites`/`operator`/`executor` (`workflow.go:218`;
  `cmd_story_review.go:410,450,463,546,694`) — a FIXED three-role engine protocol a
  repo cannot rename, comparable to `pass`/`fail` edge tokens. **Legitimate engine
  protocol**, but the nearest-to-line bake; decide deliberately if actor names
  should become repo-authorable.
- **`SummaryReviewerName="satellites-story-summary"`** (`summary_hook.go:21`) — the
  binary binds the summary hook to one reviewer name. **Constitution-sanctioned by
  name**, but note it is a named-reviewer bake the constitution had to explicitly
  bless.
- **`satellites-selfcheck-review` `check: "test -f go.mod"`** (`config/skills/…:6`) —
  a Go-host assumption shipped in the embed. **Legitimate** (it is the load-bearing
  fixture of `TestEmbeddedGateInjectedAndUsed`, wired into no shipped workflow edge),
  but a repo-agnostic check (`test -d .`) would be safer.

---

## Confirmed repo-agnostic surfaces (cleared — a re-run may skip these)

- **`satellites-` prefix derivation** — `reviewSkillForKind`, `authoringSkillForKind`,
  `localSkillName` — constitution explicitly code-sanctions the prefix.
- **`IsReviewer`** (`request_review_dispatcher.go:236,240`) — frontmatter `kind`
  classifier; the code comment cites the constitution. Sanctioned.
- **Typed model enums** — `Scope`, document `Type`, `Transport`, project/API-key
  roles, v2 edge outcomes (`pass`/`fail`), ledger kinds, trace status enum — data
  model/protocol, not per-repo substrate process names.
- **Command-owned helper skills** — `semanticReviewSkill`, `workflowDesignSkill` (a
  CLI command naming its own tool); `surfaceDocName` (overridable default).
- **Authorization lanes** — `systemChangelogWriteAllowed`/`mcpForbidsType`/
  `mcpReadForbiddenDoc` (type/scope/transport); tag-FAMILY prefix extraction
  (`kind:`/`principles:`).
- **Hook doors & engine** — START door (`cmd_hook.go`, engagement-required-to-mutate,
  `Editable` via `IsEditable`); Stop goal-keeper (`cmd_hook_stopcheck.go`, terminal
  via `IsTerminal`); v2 dispatcher (`v2_edges.go`, all targets/bounds/reviewers read
  from the workflow); gate run path (`request_review_dispatcher.go`); surface gate
  (`cmd_surface.go`); workflow drift validator (`cmd_workflow_check.go`); `work init`
  single-open/collision/actor-handoff. All derive decisions from substrate.
- **No baked version-bump / changelog-required / debt-threshold gate** in Go
  (confirmed by search).

---

## Prioritised remediation (one story per group)

| Pri | Family | Fix | Story |
|---|---|---|---|
| 1 | **F1** `commit-push` bake | commitgate enforces only editable-engagement; drop the ship-step literal binding; one-commit enforcement → substrate reviewer (not baseline) | **sty_028c3f92** (created) |
| 2 | **F3** status-vocabulary family (11 sites) | route every site through `IsTerminal`/`InitialState`/`TerminalStates`/`IsEditable` | TODO |
| 3 | **F2** kind→gate selector | resolve entry reviewer from the workflow's entry edge | TODO |
| 4 | **F4** category override | drive override authority from config, not a `"parent"` literal | TODO |
| 5 | **F5** `agent-goals` overreach | distil to repo-agnostic core; move release specifics to project substrate (with tsk_227df4a4) | TODO |
| — | Borderline | deliberate decision on actor-vocab; `selfcheck` → repo-agnostic check | optional |

F1 and F3 are highest-leverage: F1 unbricks the default workflow for every adopter;
F3 removes the largest class and has a drop-in engine mechanism already in the
codebase.

---

## Verification

- Every finding cites a concrete `file:line` and the constitution clause crossed. ✓
- Each is classified TRUE-violation vs legitimate-mechanism with a one-line
  justification; ~20 enum false positives from a parallel sweep were re-verified and
  discarded (no sanctioned mechanism flagged). ✓
- The known `commit-push` finding is present (Family 1), **detected by independent
  search** (sweeps 1 & 3), with the inter-sweep classification dissent adjudicated. ✓
- A fix story for it exists and is named (sty_028c3f92). ✓
- Confirmed-repo-agnostic surfaces enumerated so a re-run skips re-litigating
  sanctioned mechanism. ✓
- **Search-context adequacy:** independent sweeps surfaced the seeded violation **and
  ~13 analogous ones** — context does **not** need widening for this class. A future
  re-run should re-scan after F1-F5 land; the TRUE list should shrink. ✓

**Outcome:** a concrete, classified, prioritised remediation plan (5 stories;
sty_028c3f92 already created) to restore satellites to mechanism-in-the-binary /
behaviour-in-the-substrate, with the binary surfaces confirmed clean so re-runs stay
cheap.
