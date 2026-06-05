---
name: satellites_mcp_reference_skill_sync
scope: system
tags: [kind:mcp-reference]
---
# satellites · reference: Claude Code skill sync

Retired as an agent contract. Skill reconciliation is a **client** function, not
an agent one — do not hand-reconcile.

Run **`satellites skill sync`** to materialise the substrate's `type:"skill"`
rows into `.claude/skills/<name>/SKILL.md`. One pull-only pass covers every scope
the repo can see (system + workspace + project, most-specific body winning a name
collision), keyed by an injected identity stamp: install / update / skip, and
removal only for a stamp owned by no scope — never clobbering an operator-edited
or operator-authored skill. The local copy carries the `satellites-` prefix.
