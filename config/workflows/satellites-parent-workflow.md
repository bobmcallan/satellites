---
name: satellites-parent-workflow
scope: system
kind: workflow
tags: [kind:workflow]
applies_to: [parent]
description: The default lifecycle a `parent` (epic/anchor) story follows — backlog → done (or cancelled). DONE is gated by satellites-parent-close-review, which advances only when every child has reached a terminal status; CANCEL is gated by satellites-parent-cancel-review, which abandons the anchor on a concrete rationale. Both reviewers are system substrate resolved from the binary embed. Invoke for an anchor story that groups children and carries no executable work of its own.
---

# Parent (epic / anchor) workflow

An anchor story has no executable work of its own; its contract is "every child
is terminal". Two SYSTEM reviewers (see [[reviewer-only-model]]) bracket it, both
authored in `config/skills`, embedded in the client binary, resolved from embed:

- **DONE** (`backlog → done`) is gated by `satellites-parent-close-review`, which
  advances only when every child story has reached a terminal status; and
- **CANCEL** (`backlog → cancelled`) is gated by `satellites-parent-cancel-review`,
  which abandons the anchor on a concrete `## Cancellation` rationale (cancellation
  is orthogonal to the close contract — it does not require children terminal).

1. `document_get` the anchor; confirm its body names the children it groups.
2. To CLOSE: `satellites story status_transition <story-id> --skill
   satellites-parent-close-review`. To ABANDON: add a `## Cancellation` rationale
   to the body, then `satellites story status_transition <story-id> --skill
   satellites-parent-cancel-review`.

Only the reviewer advances status — never hand-patch it.

## Workflow

```yaml
states:
  - backlog
  - done
  - cancelled
transitions:
  - {from: backlog, to: done, reviewer_skill: "satellites-parent-close-review"}
  - {from: backlog, to: cancelled, reviewer_skill: "satellites-parent-cancel-review"}
```

## Environment

Runs against satellites story documents via `document_get` and `status_transition`.

```yaml
guardrails:
  always:
    - Route a close through satellites-parent-close-review and an abandon through satellites-parent-cancel-review.
  ask_first: []
  never:
    - Hand-patch story status.
    - Upsert any plan or executable work onto a parent story.
```
