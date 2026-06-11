---
name: satellites-story-plan-review
type: skill
kind: gate
when: status==backlog
tags: [kind:gate]
description: Gate skill for the entry-to-work transition (e.g. backlog → in_progress). Decides whether a story has a sound, executable plan before an executor picks it up. Emits {decision, notes} JSON.
---

Decide whether the story is a sound contract — its workflow is correctly embedded and its plan is ready to execute. Gate the contract, not the work: do not run the build or tests, and do not penalise the absence of code.

## Input

One JSON object on stdin carrying `story_id`, `project_id`, `workspace_id`, `story_status` (current state), and `story_body` (markdown containing a `## Workflow` fenced yaml block). No `next_status` — resolve the target yourself (see *Enact*).

The gate's `.satellites/satellites exec` calls authenticate as the operator's admin user, authorized to write status_transition / review_* rows. Read related rows with `.satellites/satellites exec document_get` / `ledger_list` when the body is not enough.

## Environment & guardrails

This gate runs as the operator's admin user and writes authoritative status changes. Its only write surface is `ledger_append`; all reads are `context review` / `document_get` / `ledger_list`.

```yaml
guardrails:
  always:
    - Resolve to_status only from the story's own ## Workflow transition matching (from == story_status AND reviewer_skill == satellites-story-plan-review).
    - Pair every accept with exactly two ledger_append rows: review_accept then status_transition.
    - Fail closed — if the status_transition ledger_append errors, treat the transition as not landed and print reject with the failure as the reason.
    - On accept, compute a rough time and token ESTIMATE for executing the story and carry it in the review_accept payload (estimate.time_minutes, estimate.tokens, estimate.basis). The estimate is advisory — never reject for its value.
    - Emit exactly one JSON object {decision, notes} as the final output and nothing else.
  ask_first: []
  never:
    - Never write a status_transition row on reject, or when no matching workflow transition exists.
    - Never invent or default a to_status not named by a matching transition.
    - Never write outside ledger_append (no document_upsert or other mutating exec).
    - Never run the build or tests, or penalise the absence of code — this gate reviews the contract, not the work.
```

## What to check

### 1. The embedded workflow

The body must carry a `## Workflow` section. Validate that it is SOUND, not that it equals a canonical block (per `no-default-workflow`, a workflow is designed from the requirement). Run the structural review:

```sh
.satellites/satellites context review <story_id> --json --no-ledger
```

Reject when it reports any of: `workflow-unparseable` (no parseable yaml block), `workflow-lifecycle` (degenerate — no initial/terminal state, or initial == terminal), or `missing-gate-skill` (a transition names a `reviewer_skill` not materialised in `.claude/skills`). Otherwise the workflow is sound — accept whether it is the default or a per-story design.

Advisory (do not reject): if the workflow is the category default copied verbatim with no design rationale in the body, note that in your accept `notes`.

### 2. The plan

Accept when the body answers all of:
- **Purpose** — why the change exists.
- **Approach** — a concrete plan (which area changes, the shape, any sequencing). "Investigate X" is not a plan.
- **Acceptance criteria** — numbered, testable outcomes; each names an observable behaviour, a file/path, or a measurable threshold.

Reject when the plan is missing, vague ("improve performance"), or so underspecified an executor must guess the design.

Each acceptance criterion must be satisfiable at THIS story's own completion. A criterion that depends on another, not-yet-delivered story belongs in that enabler story, not here — the done-gate checks every AC literally and fails closed, so a deferred bullet guarantees a later false reject. Reject and name the offending criterion.

Accept only when BOTH the embedded workflow validates AND the plan is sound.

### 3. Estimate (advisory — never gates)

After you have decided the plan is sound, produce a rough ESTIMATE of what executing this story will cost:

- **Time** — wall-clock minutes for an agent executor to take the story from here to its terminal state.
- **Tokens** — approximate model tokens consumed reaching that state.

Base it on the plan's scope: the number and breadth of areas touched, the acceptance-criteria count, the story kind (spike / bug / feature / improvement), and any sequencing. State the basis in one short phrase. This is a heuristic estimate, not a measurement — actual/calculated time and token tracking will be added to the reviewers later; until then this is the only signal.

The estimate NEVER affects the decision: do not reject a story for a high or low estimate. On accept, carry it in the review_accept payload (see *Enact*) and summarise it in `notes`.

You enact your decision, you do not just report it.

Resolve your target from the story's `## Workflow`: parse its `transitions`, find the one whose `from == story_status` AND `reviewer_skill == satellites-story-plan-review` (this gate's own name); its `to` is your `to_status`. If no such transition exists, reject (append `review_reject` below, print reject). Never invent a `to_status`.

Run these with Bash before printing your decision.

**On accept** — two `ledger_append` calls (no document_upsert):

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_accept","body":"<your notes>","payload":{"from_status":"<story_status>","to_status":"<to_status>","gate":"satellites-story-plan-review","estimate":{"time_minutes":<n>,"tokens":<n>,"basis":"<one phrase>"}}}'
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"status_transition","body":"<story_status> → <to_status>","payload":{"from_status":"<story_status>","to_status":"<to_status>"}}'
```

The status_transition row IS the status change; the status field on document_upsert is ignored.

**On reject** — record only the rejection, no status_transition:

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_reject","body":"<your notes>","payload":{"from_status":"<story_status>","gate":"satellites-story-plan-review"}}'
```

If the status_transition `ledger_append` fails, the transition did not land — print `reject` with the failure as the reason.

## Output

After enacting, print exactly one JSON object and nothing else — no prose, no fence:

```json
{"decision": "accept", "notes": "one or two sentences of rationale"}
```

`decision` is `accept` or `reject`. On reject, `notes` must name the specific gap to close.
