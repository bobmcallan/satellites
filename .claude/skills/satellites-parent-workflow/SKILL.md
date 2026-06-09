<!-- satellites-sync:begin {"document_id":"doc_ef46b20c","version":3,"hash":"e469205a622ef102dab51609ef1ca73079490fd052bf5341a15a56ed3966dd17"} satellites-sync:end -->
---
name: satellites-parent-workflow
type: skill
kind: workflow
tags: [kind:workflow]
applies_to: [parent]
description: The lifecycle a `parent` (epic/anchor) story follows — backlog → done, gated by satellites-parent-close-review which assesses that every child has reached a terminal status. Invoke for an anchor story that groups children and carries no executable work of its own.
---

# Parent (epic / anchor) workflow

The process for a `parent` story — an anchor that groups children and carries no executable work of its own. Its "work" is that every child has reached a terminal status; one gated transition closes it.

1. `document_get` the anchor; confirm its body names the children it groups.
2. `document_upsert` a **`## Workflow`** section: the fenced ```yaml block below, copied verbatim. There is no separate plan — the contract IS "every child is terminal".
3. Request the close gate: `satellites story status_transition <story-id> --skill satellites-parent-close-review`. It assesses the children and, when every one is terminal, enacts `backlog → done`.

Only reviewers advance status — never hand-patch it.

## Transitions

- `backlog → done` — `satellites-parent-close-review`: at least one child, every child terminal. Not a build/test gate.

```yaml
states:
  - backlog
  - done
transitions:
  - {from: backlog, to: done, reviewer_skill: "satellites-parent-close-review"}
```
