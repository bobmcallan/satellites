---
name: no-default-workflow
tags: [principles:global, area:workflow, area:reviewer]
---

# No default workflow — design the lifecycle from the requirement

Design a story's `## Workflow` from that story's requirement; the category
default is a starting point, not the answer. The workflow must gate every step
the story's requirements and the applicable principles demand — anything
required must be a gated transition, not prose (see [[enforcement-contract]]).

Keeping the default is allowed only when it genuinely fits — a deliberate
decision the story body justifies, not a reflex. A verbatim default copied with
no design rationale is flagged. plan-review accepts any sound, valid workflow —
default or designed — provided it passes lifecycle validation and names only
materialised gate skills.
