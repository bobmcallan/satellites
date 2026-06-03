---
name: principle-configuration-over-code
tags: [principles:project]
---
# Configuration over code

When a substrate behaviour can be expressed as data — a markdown
artifact, a tag, a row in a configurable store — write it as data, not
as a Go branch. Code is the load layer; the substance lives in
documents, principles, workflow skills, and reviewer rubrics that
operators can author and edit.

## What this rules in

- Agent-facing prose ships as markdown artifacts under
  `config/documents/` or as substrate documents — never as string
  constants in Go.
- Workflow phases, reviewer mappings, and per-story rules live in
  configuration documents the operator reads and edits — never as hard-
  coded switch statements.
- Principles, rubrics, and process rules are documents the substrate
  delivers via ride-along — never special-cased load steps.

## What this rules out

- Inlining markdown prose into Go source.
- Hard-coded enum branches for new substrate kinds when a tag or scope
  would do.
- Bespoke verbs for what an existing `document_*` verb already covers.

## When you have a choice

Prefer the option that an operator can change without a binary release.
If the rule must ship with the binary (system-scope), let it ship as a
seeded artifact rather than a Go constant. If it must be enforced at
runtime, that is the reviewer's job — encode it in a reviewer skill,
not in the verb that the agent calls.
