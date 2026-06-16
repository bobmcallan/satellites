---
name: satellites-parent-workflow
kind: workflow
tags: [kind:workflow]
applies_to: [parent]
description: The lifecycle a `parent` (epic/anchor) story follows — backlog → done, gated by satellites-parent-close-review which assesses that every child has reached a terminal status. Invoke for an anchor story that groups children and carries no executable work of its own.
---

# Parent (epic / anchor) workflow

1. `document_get` the anchor; confirm its body names the children it groups.
2. Run `satellites workflow embed <story-id>` to stamp the **`## Workflow`** section (resolved by the `parent` category). There is no separate plan — the contract IS "every child is terminal".
3. Request the close gate: `satellites story status_transition <story-id> --skill satellites-parent-close-review`. It assesses the children and, when every one is terminal, enacts `backlog → done`.

Only reviewers advance status — never hand-patch it.

## Workflow

- `backlog → done` — `satellites-parent-close-review`: at least one child, every child terminal. Not a build/test gate.

```yaml
states:
  - backlog
  - done
transitions:
  - {from: backlog, to: done, reviewer_skill: "satellites-parent-close-review"}
```

## Environment

Runs against satellites story documents via `document_get`, `document_upsert`, and `status_transition`. State-mutating: it writes the `## Workflow` section and triggers a gated transition.

```yaml
guardrails:
  always:
    - route every status change through the reviewer skill (satellites-parent-close-review)
    - copy the Workflow yaml block verbatim
  ask_first: []
  never:
    - hand-patch story status
    - upsert any plan or executable work onto a parent story
```
