---
name: satellites-intent-plan-review
type: skill
kind: reviewer
when: plan
tags: [kind:reviewer]
description: The spine plan gate — judges that a story is satellites-formatted (Purpose, Approach, numbered acceptance criteria), carries a clear story→done goal, and records a RESOLVABLE governing workflow (a valid `workflow:` selector, or a category that resolves a default), before an executor starts. It NO LONGER requires an embedded ## Workflow — the process comes from the selector or the category default. It does NOT judge config-over-code (that is satellites-intent-code-review, on the diff) nor re-judge code grounding. Emits {decision, notes} JSON.
---

Decide ONE thing: is this story a well-formed, drivable contract — **satellites-formatted, with a clear story→done goal and a resolvable governing workflow**? This is the minimal spine gate every repo gets; it ensures the agent picks up a story it can actually drive to done. It does NOT judge config-over-code (satellites-intent-code-review owns that, on the diff) and does NOT re-judge code grounding (a comprehensive plan-review, where the repo runs one, owns that).

## Input

One JSON object on stdin carrying `story_id`, `project_id`, `workspace_id`, `story_status` (current state), and `story_body` (markdown). The story body need NOT carry a `## Workflow` block — the governing workflow is resolved from the story's recorded selector or its category default (see *Workflow selection* and *Enact*). No `next_status` — resolve the target yourself.

The gate's `.satellites/satellites exec` calls authenticate as the operator's admin user, authorized to write status_transition / review_* rows.

## Decision rule

Judge the `story_body` and the story's **workflow selection** (below):

- **accept** — the story is satellites-formatted, has a clear story→done goal, AND resolves a governing workflow:
  - a **Purpose** (why this story exists) and an **Approach** (how it will be done),
  - **numbered acceptance criteria** that are concrete and testable at the story's completion,
  - a coherent goal that a single story can drive to a terminal `done` (not an open-ended programme, not a vague aspiration), and
  - a **resolvable governing workflow** — see *Workflow selection*. An embedded `## Workflow` block is OPTIONAL (display only): when present it is not re-judged as the authority; when absent the selector or category default governs.
- **reject** — the story is malformed, its goal is not done-able, or its workflow selection is invalid: missing Purpose/Approach, no numbered acceptance criteria (or vague/untestable ones), a goal too broad/ambiguous to reach `done`, OR a `workflow:` selector that names no workflow in the source set / names one that does not match the story's category (see *Workflow selection*). Name exactly what is missing or unfollowable.

Fail closed: if the story body cannot be read or its shape cannot be judged, reject with the reason named.

## Workflow selection

The governing process is the workflow the story RECORDS BY NAME, not an embedded
copy. Judge the selection with read-only commands (you may run them with Bash):

1. Read the story's selector: `satellites story get <story_id>` — the
   `workflow:<name>` tag, if any, is the recorded choice.
2. List the palette: `satellites workflow list <story_id>` — the workflows whose
   `applies_to` covers the story's category, ranked (the top row is the default).
3. Decide:
   - **A selector IS set** → it must appear in the palette (names a real workflow
     that covers the category). If it does → the selection is valid. If it names
     no listed workflow, or one that does not cover the category → **reject**,
     naming the bad selector and pointing at `workflow list`.
   - **No selector** → the category DEFAULT (the top palette row) governs. If the
     palette is non-empty → valid (the default is well-defined). If the palette is
     EMPTY (no workflow covers the category) → **reject** (the story has no
     resolvable process; author a workflow or set a valid selector).

You JUDGE the selection only — you do NOT set or change the `workflow:` tag. On a
reject the executor re-selects (via `workflow list`) and re-requests this gate.

## Environment

You are a reviewer. You read the story body; your only writes are the named ledger rows below — no `document_upsert`, no git/file mutation.

```yaml
guardrails:
  always:
    - Judge ONLY story format (Purpose/Approach/numbered AC), a clear story→done goal, and a resolvable governing-workflow selection (a valid workflow: selector or a non-empty category palette).
    - Resolve to_status from the GOVERNING workflow (the recorded selector, else the category default) — the transition whose from == story_status AND reviewer_skill == satellites-intent-plan-review; the embedded ## Workflow, if present, is display only.
    - Pair every accept with exactly two ledger_append rows: review_accept then status_transition.
    - Fail closed — if the status_transition ledger_append errors, treat the transition as not landed and print reject with the failure as the reason.
    - Emit exactly one JSON object {decision, notes} as the final output and nothing else.
  ask_first: []
  never:
    - Judge config-over-code or hardcoding intent — that is satellites-intent-code-review, on the diff.
    - Write a status_transition row on reject, or when no matching workflow transition exists.
    - Invent or default a to_status not named by a matching transition.
    - Write outside ledger_append (no document_upsert or other mutating exec).
```

## Enact

You enact your decision, you do not just report it.

Resolve your target from the GOVERNING workflow — the one the story selects (the
`workflow:<name>` tag), else the category default (the top row of
`satellites workflow list <story_id>`). Render it with
`satellites workflow show <name>` and find the transition whose
`from == story_status` AND `reviewer_skill == satellites-intent-plan-review`
(this gate's own name); its `to` is your `to_status`. (If the story still embeds a
`## Workflow` it should match the governing one — the governing definition is the
authority.) If no such transition exists in the governing workflow, reject
(append `review_reject` below, print reject). Never invent a `to_status`.

Run these with Bash before printing your decision.

**On accept** — two `ledger_append` calls (no document_upsert):

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_accept","body":"<your notes>","payload":{"from_status":"<story_status>","to_status":"<to_status>","gate":"satellites-intent-plan-review"}}'
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"status_transition","body":"<story_status> → <to_status>","payload":{"from_status":"<story_status>","to_status":"<to_status>"}}'
```

The status_transition row IS the status change; the status field on document_upsert is ignored.

**On reject** — record only the rejection, no status_transition:

```sh
.satellites/satellites exec ledger_append --json '{"story_id":"<story_id>","project_id":"<project_id>","workspace_id":"<workspace_id>","kind":"review_reject","body":"<your notes>","payload":{"from_status":"<story_status>","gate":"satellites-intent-plan-review"}}'
```

If the status_transition `ledger_append` fails, the transition did not land — print `reject` with the failure as the reason.

## Output

After enacting, print exactly one JSON object and nothing else — no prose, no fence:

```json
{"decision": "accept", "notes": "one or two sentences; on reject, name exactly what is missing (Purpose/Approach/AC/## Workflow) or why the goal is not done-able"}
```

`decision` is `accept` or `reject`. On reject, `notes` must name the specific format gap or unfollowable goal.
