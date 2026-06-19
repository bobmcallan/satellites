---
name: principle-configuration-over-code
tags: [principles:project]
---
# Configuration over code

When a substrate behaviour can be expressed as data — a markdown artifact, a
tag, a row in a configurable store — write it as data, not a Go branch. Code is
the load layer; the substance lives in documents, principles, workflow skills,
and reviewer rubrics that operators can author and edit without a binary release.

- Agent-facing prose ships as markdown artifacts or substrate documents — never
  as string constants in Go.
- Workflow phases, reviewer mappings, and per-story rules live in configuration
  documents — never hard-coded switch/enum branches.
- A rule enforced at runtime is the reviewer's job — encode it in a reviewer
  skill, not in the verb the agent calls.
- Which reviewer or gate runs is itself configuration — resolve it at runtime (a
  kind is gated IFF its reviewer EXISTS in the substrate), never a hardcoded kind
  list or switch. Authoring a reviewer turns its gate on with no release.

See [[constitution]], [[reviewer-only-model]].
