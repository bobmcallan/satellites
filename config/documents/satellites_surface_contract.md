---
name: satellites_surface_contract
type: document
scope: system
headline: MCP is the intent surface (documents/epics/stories, created freely, no upsert review); the client is the engineering surface (refinement, skills, principles, gates) — the workflow's first gate is where a story is judged
tags: [area:substrate, kind:contract]
---

# Surface contract — who writes what, where it is judged

The server runs no agent; reviews exist only where the client's `claude -p`
runs. The write surfaces divide accordingly.

## MCP — the intent surface

For the people and tools that express intent: delivery manager, project
manager, customer, planning integrations.

- **May create and update freely:** stories and tasks (intent), epics
  (anchors), and plain reference documents. A story create carries **no
  review** — intent needs no agent's permission to exist.
- **May not touch executable configuration:** writes, reads, and deletes of
  skills and principles are refused with a pointer to the client path. A
  reviewer's judgment cannot run server-side, so nothing requiring one is
  accepted there.
- Status moves only through reviewer gates (`status_transition` ledger rows) —
  never by patching the field.

## Client — the engineering surface

For the agent and operator working a repository.

- **Refinement is judged at the workflow's FIRST gate**, not on upsert: when a
  story is picked up, the entry reviewer judges everything in one verdict —
  story shape, plan, acceptance criteria, the workflow SELECTION (the recorded
  `workflow:` selector, or the category default — not a hand-embedded
  `## Workflow`, which is no longer required at create/upsert), and code
  grounding. Nothing unreviewed can progress; creation needs no second
  mechanism. The standing rule is the `review-actions-not-intent` principle;
  this contract only records where each write lands.
- **Reviewable artifacts ship only through the review-gated upload path, ROUTED
  by type.** `satellites skill|principle upload` and `workflow upsert` resolve the
  per-type reviewer (`satellites-<kind>-review`) and run it before the write — a
  kind is gated IFF its reviewer EXISTS (config-over-code), so a plain `document`
  (no reviewer) passes through while a principle/skill/workflow cannot be written
  unreviewed. An optional `[name]` uploads ONE artifact, decoupled from a
  sibling's reject. The MCP door refuses these writes outright (no server-side
  reviewer), making the client the single reviewer-gated home. Deletes of these
  artifacts are likewise client-side.
- Gates execute locally (`claude -p`) against the materialised skill set.

## Direction of travel

The MCP surface trends toward documents-only; tightening which types it
accepts is configuration on the verb surface, not a rearchitecture.
