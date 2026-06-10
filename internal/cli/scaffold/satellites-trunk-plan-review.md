---
name: satellites-trunk-plan-review
type: skill
kind: gate
when: status==backlog
tags: [kind:gate]
description: Scaffolded entry gate for the backlog → in_progress transition. Decides whether a story has a sound, embedded workflow and an executable plan before work starts. Emits {decision, notes} JSON. Edit it to fit your review bar.
---

Decide whether the story is a sound contract — its workflow is correctly embedded
and its plan is ready to execute. Gate the contract, not the work: do not run the
build or tests, and do not penalise the absence of code. This gate is a SCAFFOLD
`satellites init` wrote — tune the checks below to your standard.

## Input

One JSON object on stdin carrying `story_id`, `project_id`, `workspace_id`,
`story_status` (current state), and `story_body` (markdown containing a
`## Workflow` fenced yaml block). No `next_status` — resolve the target yourself
(see *Enact*).

The gate's `.satellites/satellites exec` calls authenticate as the operator's
admin user, authorized to write status_transition / review_* rows. Read related
rows with `.satellites/satellites exec document_get` / `ledger_list` when the body
is not enough.

## Environment & guardrails

This gate runs as the operator's admin user and writes authoritative status
changes. Its only write surface is `ledger_append`.

```yaml
guardrails:
  always:
    - Resolve to_status only from the story's own ## Workflow transition matching (from == story_status AND reviewer_skill == satellites-trunk-plan-review).
    - Pair every accept with exactly two ledger_append rows: review_accept then status_transition.
    - Fail closed — if the status_transition ledger_append errors, treat the transition as not landed and print reject with the failure as the reason.
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

The body must carry a `## Workflow` section. Validate that it is SOUND (parseable,
a non-degenerate lifecycle, and every `reviewer_skill` it names is materialised in
`.claude/skills`):

```sh
.satellites/satellites context review <story_id> --json --no-ledger
```

Reject when it reports `workflow-unparseable`, `workflow-lifecycle`, or
`missing-gate-skill`. Otherwise the workflow is sound.

### 2. The plan

Accept when the body answers all of:
- **Purpose** — why the change exists.
- **Approach** — a concrete plan (which area changes, the shape, any sequencing).
- **Acceptance criteria** — numbered, testable outcomes, each naming an observable
  behaviour, a file/path, or a measurable threshold.

Reject when the plan is missing, vague, or so underspecified an executor must
guess the design. Each acceptance criterion must be satisfiable at THIS story's
own completion.

Accept only when BOTH the embedded workflow validates AND the plan is sound.

## Enact

You enact your decision, you do not just report it.

Resolve your target from the story's `## Workflow`: parse its `transitions`, find
the one whose `from == story_status` AND `reviewer_skill ==
satellites-trunk-plan-review` (this gate's own name); its `to` is your
`to_status`. If no such transition exists, reject. Never invent a `to_status`.

Run these with Bash before printing your decision.

**On accept** — two `ledger_append` calls (no document_upsert):

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_accept","body":"<your notes>","payload":{"from_status":"<story_status>","to_status":"<to_status>","gate":"satellites-trunk-plan-review"}}'
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"status_transition","body":"<story_status> → <to_status>","payload":{"from_status":"<story_status>","to_status":"<to_status>"}}'
```

The status_transition row IS the status change.

**On reject** — record only the rejection, no status_transition:

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_reject","body":"<your notes>","payload":{"from_status":"<story_status>","gate":"satellites-trunk-plan-review"}}'
```

If the status_transition `ledger_append` fails, the transition did not land —
print `reject` with the failure as the reason.

## Output

After enacting, print exactly one JSON object and nothing else — no prose, no fence:

```json
{"decision": "accept", "notes": "one or two sentences of rationale"}
```

`decision` is `accept` or `reject`. On reject, `notes` must name the specific gap to close.
