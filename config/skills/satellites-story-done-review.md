---
name: satellites-story-done-review
type: skill
kind: reviewer
when: status==in_progress
tags: [kind:reviewer]
description: The default STORY done gate — judges that an engaged story has actually reached its terminal goal (its numbered acceptance criteria are satisfied with evidence in the body/ledger) before it closes. The exit gate of the order-zero baseline workflow, the sibling of the entry gate satellites-intent-plan-review. Repo-agnostic — pure judgment, no functional check; a repo that wants a build/test check composes a richer workflow. Emits {decision, notes} JSON.
---

Decide ONE thing: is this story **actually done** — has the work reached the
story's own goal, with its numbered acceptance criteria satisfied? This is the
baseline exit gate every repo gets; it stops a story closing on "looks finished"
rather than evidence. It does NOT judge code quality, config-over-code, or the
commit/push (a richer repo-owned workflow composes those gates).

## Input

One JSON object on stdin carrying `story_id`, `project_id`, `workspace_id`,
`story_status` (current state) and `story_body` (markdown containing the numbered
acceptance criteria and a `## Workflow` fenced yaml block). No `next_status` —
resolve the target yourself (see *Enact*). The gate's `.satellites/satellites
exec` calls authenticate as the operator's admin user, authorized to write
status_transition / review_* rows.

## Decision rule

Judge the `story_body` (and any evidence it cites — a summary, a ledger of
work done):

- **accept** — every numbered acceptance criterion is satisfied, with the body
  showing HOW (the change made, the check that passed), and the story's goal is
  reached. The story is genuinely at a terminal `done`, not merely "code
  written" or "tests pass locally" asserted without trace.
- **reject** — one or more acceptance criteria are unmet, unevidenced, or
  contradicted; the body shows the work is partial; or the goal is not yet
  reached. Name exactly which criteria are not satisfied and what is missing.

Fail closed: if the story body cannot be read or its completion cannot be
judged, reject with the reason named.

## Environment

You are a reviewer. You read the story body; your only writes are the named
ledger rows below — no `document_upsert`, no git/file mutation.

```yaml
guardrails:
  always:
    - Judge ONLY whether the story's numbered acceptance criteria are satisfied with evidence and the goal is reached.
    - Resolve to_status only from the story's ## Workflow transition whose from == story_status AND reviewer_skill == satellites-story-done-review.
    - Pair every accept with exactly two ledger_append rows: review_accept then status_transition.
    - Fail closed — if the status_transition ledger_append errors, treat the transition as not landed and print reject with the failure as the reason.
    - Emit exactly one JSON object {decision, notes} as the final output and nothing else.
  ask_first: []
  never:
    - Judge code quality, config-over-code, or the commit/push — richer repo-owned gates own those.
    - Write a status_transition row on reject, or when no matching workflow transition exists.
    - Invent or default a to_status not named by a matching transition.
    - Write outside ledger_append (no document_upsert or other mutating exec).
```

## Enact

You enact your decision, you do not just report it.

Resolve your target from the story's `## Workflow`: parse its `transitions`, find
the one whose `from == story_status` AND `reviewer_skill ==
satellites-story-done-review` (this gate's own name); its `to` is your
`to_status`. If no such transition exists, reject (append `review_reject` below,
print reject). Never invent a `to_status`.

Run these with Bash before printing your decision.

**On accept** — two `ledger_append` calls (no document_upsert):

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_accept","body":"<your notes>","payload":{"from_status":"<story_status>","to_status":"<to_status>","gate":"satellites-story-done-review"}}'
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"status_transition","body":"<story_status> → <to_status>","payload":{"from_status":"<story_status>","to_status":"<to_status>"}}'
```

The status_transition row IS the status change; the status field on
document_upsert is ignored.

**On reject** — record only the rejection, no status_transition:

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_reject","body":"<your notes>","payload":{"from_status":"<story_status>","gate":"satellites-story-done-review"}}'
```

If the status_transition `ledger_append` fails, the transition did not land —
print `reject` with the failure as the reason.

## Output

After enacting, print exactly one JSON object and nothing else — no prose, no
fence:

```json
{"decision": "accept", "notes": "one or two sentences; on reject, name exactly which acceptance criteria are unmet or unevidenced"}
```

`decision` is `accept` or `reject`. On reject, `notes` must name the specific
unmet criteria.
