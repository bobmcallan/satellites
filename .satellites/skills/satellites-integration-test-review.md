---
name: satellites-integration-test-review
type: skill
kind: gate
when: status==integration-review
tags: [kind:gate]
description: Gate skill for the integration-review state (after techdebt-review pass, before done-review). Judges whether UI/DOGFOOD acceptance criteria are evidenced by the repo's own integration tier — named tests in tests/integration/, run green by the techdebt traverse, conforming to the tier's architecture; trivial accept when the story has no browser/UI surface. Emits {decision, notes} JSON.
---

Judge whether the story's UI/DOGFOOD acceptance criteria are evidenced by the
repo's OWN integration tier — named tests in `tests/integration/`, run green by
the techdebt traverse — never by a side harness, a manual walkthrough, or a
foreign browser stack. This gate is the single home of that rule: no principle
restates it.

## Input

One JSON object on stdin carrying `story_id`, `project_id`, `workspace_id`,
`story_status` (current state), and `story_body` (markdown containing a
`## Workflow` fenced yaml block). No `next_status` — resolve the target
yourself (see *Enact*).

## Decision rule

First identify the story's **UI/DOGFOOD criteria**: acceptance criteria that
claim a browser-visible behaviour, a portal/UI change, or an explicit
DOGFOOD/dogfood verification.

- **accept (trivial)** — the story has NO UI/DOGFOOD criteria (no browser
  surface). Nothing is in this gate's scope; say so in the notes.
- **accept** — every UI/DOGFOOD criterion holds on all three judgments:
  1. **Coverage** — it maps to a NAMED test in `tests/integration/`
     (the test exists in the tree and asserts that criterion's behaviour).
  2. **Execution** — the techdebt traverse ran the tier green over the change:
     the story's ledger carries a CLEAN techdebt/ci_result verdict row from the
     traverse, and the named tests are part of that tier (not skipped or
     quarantined in the technical-debt register).
  3. **Architecture** — the test follows the tier's conventions (chromedp for
     browser assertions, the tier's helpers/structure) and asserts the
     criterion's behaviour — not a smoke no-op that merely loads a page.
- **reject** — any UI/DOGFOOD criterion evidenced only by a manual transcript,
  screenshots, prose claims, or a foreign-stack harness (Playwright or any
  second browser stack); a test that is unnamed, missing from
  `tests/integration/`, quarantined, or a smoke no-op; or tier execution that
  cannot be confirmed from rows.

**Fail closed.** If you cannot read the tests, the ledger rows, or the tier
outcome, that is a reject with the reason named — never a soft pass.

## Environment

You run in the story's worktree with admin-authenticated
`.satellites/satellites exec` access. You are a **reviewer**: read-only on the
tree; your only writes are the named ledger rows below.

```yaml
guardrails:
  always:
    - Identify the UI/DOGFOOD criteria from the story body before judging; none in scope → trivial accept.
    - Verify coverage against the real tests/integration/ tree and execution against ledger rows, not prose.
    - Fail closed — an unverifiable judgment is a reject.
    - Resolve to_status only from the story's ## Workflow transition whose reviewer_skill is this gate.
  ask_first: []
  never:
    - Modify the working tree — no edits, commits, stashes, or git/file mutation; verification is read-only.
    - Accept a side harness, manual transcript, or foreign browser stack as evidence for a UI/DOGFOOD criterion.
    - Write anything except the named ledger_append rows (review_accept, review_reject, status_transition).
    - Invent or guess a to_status that is not declared in the ## Workflow.
```

## Enact

Resolve your target from the story's `## Workflow`: parse its `transitions`,
find the one whose `from == story_status` AND `reviewer_skill ==
satellites-integration-test-review`; its `to` is your `to_status`. If no such
transition exists, reject. Never invent a `to_status`.

**Judge-only on v2 edges:** if the matched transition carries an `on:` field
(`on: pass` / `on: fail`), the CLIENT enacts — your decision selects the edge,
the client writes the review_* and status_transition rows, counts the fail-loop
bound, and escalates on exhaustion. In that case write NOTHING: print only the
decision JSON. Writing rows yourself on a v2 edge double-enacts.

Only on a legacy (no `on:`) edge, enact with the same two-row accept / one-row
reject `ledger_append` contract as [[satellites-story-done-review]], with
`"gate":"satellites-integration-test-review"` in the payload.

## Output

After judging, print exactly one JSON object and nothing else — no prose, no
fence:

```json
{"decision": "accept", "notes": "one or two sentences: which criteria were in scope and the evidence, or 'no UI/DOGFOOD surface — trivial accept'"}
```

`decision` is `accept` or `reject`. On reject, `notes` must name each failing
criterion and which judgment (coverage / execution / architecture) it failed.
