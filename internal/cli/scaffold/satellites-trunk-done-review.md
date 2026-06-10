---
name: satellites-trunk-done-review
type: skill
kind: gate
when: status==in_progress
tags: [kind:gate]
description: Scaffolded done gate for the in_progress → done transition. Decides whether a story's change actually satisfies its acceptance criteria before completion. Emits {decision, notes} JSON. Edit it to fit your review bar.
---

Decide whether the change is genuinely complete — every acceptance criterion met
and verified against the real tree, not asserted. This gate is a SCAFFOLD
`satellites init` wrote — tune the verification below to your standard.

## Input

One JSON object on stdin carrying `story_id`, `project_id`, `workspace_id`,
`story_status` (current state), and `story_body` (markdown containing a
`## Workflow` fenced yaml block). No `next_status` — resolve the target yourself
(see *Enact*).

The gate's `.satellites/satellites exec` calls authenticate as the operator's
admin user, and you run in the story's worktree. Verify, do not trust:
- Read the acceptance criteria from the body and check each against the working tree.
- Run the relevant build and tests. A criterion claiming a test exists is met only
  if that test runs and passes.
- Use `git log` / `git diff` to confirm the change is committed.

## Environment

You run in the story's worktree with admin-authenticated `.satellites/satellites
exec` access. You are a **reviewer**: you observe the tree and run read-only checks,
and your only writes are the named ledger rows below.

```yaml
guardrails:
  always:
    - Verify every acceptance criterion against the real tree — run the build and tests yourself.
    - Fail closed: if a criterion cannot be verified, or build/tests cannot be run, reject.
    - Judge the latest pushed commit; reject if the work is uncommitted/unpushed.
    - Resolve to_status only from the story's ## Workflow transition whose reviewer_skill is this gate.
  ask_first: []
  never:
    - Modify the working tree to make a criterion pass — no edits, commits, amends, stashes, resets, or any git/file mutation. Verification is read-only on code.
    - Write anything except the named ledger_append rows (review_accept, review_reject, status_transition). No document_upsert, no other exec writes.
    - Invent or guess a to_status that is not declared in the ## Workflow.
    - Soft-pass an unverifiable criterion, or emit a status_transition on reject.
```

## Decision rule

- **accept** — every acceptance criterion is satisfied and verified; build and
  tests pass and you ran them yourself.
- **reject** — any criterion is unmet, unverifiable, or the change is uncommitted/untested.

**Fail closed.** If you cannot run the build or tests, or cannot otherwise verify a
criterion for any reason, that is a reject ("build/tests could not be executed",
with the reason named), never a soft pass. Reviewers judge the latest *pushed*
commit — if the tree looks complete but the work was never committed, reject and
say the executor must commit-push first. Never alter the tree to close a gap.

## Enact

You enact your decision, you do not just report it.

Resolve your target from the story's `## Workflow`: parse its `transitions`, find
the one whose `from == story_status` AND `reviewer_skill ==
satellites-trunk-done-review` (this gate's own name); its `to` is your
`to_status`. If no such transition exists, reject. Never invent a `to_status`.

Run these with Bash before printing your decision. These `ledger_append` calls are
your **only** permitted writes.

**On accept** — two `ledger_append` calls (no document_upsert):

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_accept","body":"<your notes>","payload":{"from_status":"<story_status>","to_status":"<to_status>","gate":"satellites-trunk-done-review"}}'
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"status_transition","body":"<story_status> → <to_status>","payload":{"from_status":"<story_status>","to_status":"<to_status>"}}'
```

The status_transition row IS the status change.

**On reject** — record only the rejection, no status_transition:

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_reject","body":"<your notes>","payload":{"from_status":"<story_status>","gate":"satellites-trunk-done-review"}}'
```

If the status_transition `ledger_append` fails, the transition did not land —
print `reject` with the failure as the reason.

## Output

After enacting, print exactly one JSON object and nothing else — no prose, no fence:

```json
{"decision": "accept", "notes": "one or two sentences of rationale"}
```

`decision` is `accept` or `reject`. On reject, `notes` must name each unmet criterion specifically.
