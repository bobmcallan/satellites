---
name: satellites-parent-workflow
kind: workflow
tags: [kind:workflow]
applies_to: [parent]
description: The lifecycle a `parent` (epic/anchor) story follows — backlog → done, gated by satellites-parent-close-review which assesses that every child has reached a terminal status. Invoke for an anchor story that groups children and carries no executable work of its own.
---

# Parent (epic / anchor) workflow

An anchor story has no executable work of its own; its contract is "every child
is terminal". The single reviewer (see [[reviewer-only-model]]) is
`satellites-parent-close-review`, which advances `backlog → done` only when every
child story has reached a terminal status.

1. `document_get` the anchor; confirm its body names the children it groups.
2. Request the close reviewer: `satellites story status_transition <story-id>
   --skill satellites-parent-close-review`. It assesses the children and, when
   every one is terminal, enacts `backlog → done`.

Only the reviewer advances status — never hand-patch it.

## Workflow

```yaml
states:
  - backlog
  - done
transitions:
  - {from: backlog, to: done, reviewer_skill: "satellites-parent-close-review"}
```

## Environment

Runs against satellites story documents via `document_get` and `status_transition`.

```yaml
guardrails:
  always:
    - Route the status change through satellites-parent-close-review.
  ask_first: []
  never:
    - Hand-patch story status.
    - Upsert any plan or executable work onto a parent story.
```
