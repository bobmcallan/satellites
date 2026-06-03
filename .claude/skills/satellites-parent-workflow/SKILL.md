<!-- satellites-sync:begin {"document_id":"doc_ef46b20c","version":1,"hash":"cd2211703f09674dc91040af914414e124dbd8d6f17c1a4975fccc2fecb1aa0f"} satellites-sync:end -->
---
name: satellites-parent-workflow
type: skill
kind: workflow
tags: [kind:workflow]
applies_to: [parent]
description: The lifecycle a `parent` (epic/anchor) story follows — backlog → done, gated by satellites-parent-close-review which assesses that every child has reached a terminal status. Invoke for an anchor story that groups children and carries no executable work of its own.
---

# Parent (epic / anchor) workflow

This skill is the **process** for a `parent` story — an epic anchor, or any
story that groups children and carries no executable work of its own. Its
"work" is not code: it is the fact that every child it groups has reached a
terminal status. One gated transition closes the anchor once that holds.

## The story is the contract

An anchor records its workflow into its body like any other story:

1. Read the anchor (`document_get`) and confirm its body names the children it
   groups.
2. Record the **`## Workflow`** section: copy this skill's states + transitions
   (the fenced ```yaml block below) verbatim. There is no separate plan to
   write — the anchor's contract IS "every child is terminal", and the close
   gate checks exactly that.
3. Request the close gate: `.satellites/satellites story review <story-id>`.
   `satellites-parent-close-review` assesses the children and, when every one is
   terminal, enacts `backlog → done`.

The anchor never patches its own status; the reviewer enacts it, like every
transition. Never hand-patch status.

## Transitions

One reviewer gate. The anchor sits in `backlog` until its children are done;
`satellites-parent-close-review` is the only path to `done`. It is **not** a
build/test gate — it assesses the children's statuses and the genuine-anchor
guard (at least one child, every child terminal), nothing more.

States and transitions live in the fenced ```yaml block below. Free text around
it is for human readers — the parser only reads what's inside the block.

```yaml
states:
  - backlog
  - done
transitions:
  - {from: backlog, to: done, reviewer_skill: "satellites-parent-close-review"}
```
