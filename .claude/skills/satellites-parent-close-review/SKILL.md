---
name: satellites-parent-close-review
type: skill
kind: gate
when: status==backlog
tags: [kind:gate]
description: Gate skill for closing a parent (epic/anchor) story — backlog → done. Accepts only when the anchor has at least one child AND every child is in a terminal status; rejects a childless or still-open anchor. Emits {decision, notes} JSON.
---
<!-- satellites-sync:begin {"document_id":"doc_fcb911a6","version":4,"hash":"d59b5a9a8e9201d3e24c707492458a2dae40fef47989beeb3c79917168f6e106"} satellites-sync:end -->

Decide whether a parent (epic/anchor) story has earned closure: it is a genuine anchor (not empty or relabelled) AND every child it groups has reached a terminal status.

## Input

One JSON object on stdin carrying `story_id`, `project_id`, `workspace_id`, `story_status` (current state), and `story_body` (markdown containing a `## Workflow` fenced yaml block). No `next_status` — resolve the target yourself (see *Enact*).

The gate's `.satellites/satellites exec` calls authenticate as the operator's admin user, authorized to write status_transition / review_* rows. Use the CLI to read the children.

## What to check

Terminal statuses are `done` and `cancelled`. You assess statuses — you do not run the build or tests or re-judge the children's work. Resolve the children — every story whose `parent_id` equals your `story_id`:

```sh
.satellites/satellites exec document_list --json '{"type":"story","project_id":"<project_id>","status":"all","limit":200}'
```

Filter `items` to those whose `parent_id` equals your `story_id` (page with the response `next_cursor` if there are more than ~200 stories). Then:

- **Reject** when the anchor has zero children — "every child is terminal" must never pass vacuously over an empty set (this guards against relabelling a childless leaf to `parent` for a free close). Say so in the notes.
- **Reject** when any child is non-terminal (anything other than `done` or `cancelled`). Name each open child (`sty_<hex>`) and its status.
- **Accept** only when there is at least one child AND every child is terminal.

## Enact

You enact your decision, you do not just report it.

Resolve your target from the story's `## Workflow`: parse its `transitions`, find the one whose `from == story_status` AND `reviewer_skill == satellites-parent-close-review` (this gate's own name); its `to` is your `to_status`. If no such transition exists, reject (append `review_reject` below, print reject). Never invent a `to_status`.

Run these with Bash before printing your decision.

**On accept** — two `ledger_append` calls (no document_upsert):

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_accept","body":"<your notes>","payload":{"from_status":"<story_status>","to_status":"<to_status>","gate":"satellites-parent-close-review"}}'
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"status_transition","body":"<story_status> → <to_status>","payload":{"from_status":"<story_status>","to_status":"<to_status>"}}'
```

The status_transition row IS the status change; the status field on document_upsert is ignored.

**On reject** — record only the rejection, no status_transition:

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_reject","body":"<your notes>","payload":{"from_status":"<story_status>","gate":"satellites-parent-close-review"}}'
```

If the status_transition `ledger_append` fails, the transition did not land — print `reject` with the failure as the reason.

## Environment

Runs as a gate over the `.satellites` CLI, authenticated as the operator's admin user. It reads children via `document_list` and writes `review_accept` / `review_reject` / `status_transition` ledger rows — and only those — against the anchor `story_id` it was handed. It owns no host-repo state: no build, no tests, no files.

```yaml
guardrails:
  always:
    - Append every ledger row against the anchor story_id from the input — never against a child.
    - Resolve to_status solely from the story's ## Workflow transition whose from == story_status AND reviewer_skill == satellites-parent-close-review.
    - On accept, emit both a review_accept AND a status_transition row; the status_transition row is the status change.
    - On reject, emit a review_reject row and no status_transition.
    - Treat a failed status_transition ledger_append as a non-landed transition — print reject with the failure as the reason.
  ask_first: []
  never:
    - Invent or guess a to_status when no matching ## Workflow transition exists — reject instead.
    - Mutate, transition, re-judge, or append any row for a child story — children are read-only inputs.
    - Call document_upsert, or run the build, tests, or any host-repo command.
    - Accept vacuously over zero children, or over any non-terminal child.
```

## Output

After enacting, print exactly one JSON object and nothing else — no prose, no fence:

```json
{"decision": "accept", "notes": "one or two sentences of rationale"}
```

`decision` is `accept` or `reject`. On reject, `notes` must name the specific gap — zero children, or each non-terminal child and its status.
