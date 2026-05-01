---
name: default
tags: [v4, system]
---
# Default System Workflow

The default lifecycle every story passes through. After
`epic:configuration-over-code-mandate` (story_af79cf95) the workflow is
**prose-only context** for the orchestrator and reviewer agents — the
substrate no longer enforces the shape.

## Shape

`plan → develop → push → merge_to_main → story_close`

- `plan` — implementation strategy + review criteria. The plan agent
  also assesses readiness (relevance, dependencies, prior delivery)
  and decomposes the story into role-tagged child tasks the
  downstream contracts consume.
- `develop` — code edits + tests + commit. Multiple develop CIs are permitted when a story splits naturally (e.g. backend then frontend), but each is its own CI with its own evidence.
- `push` — ship to origin.
- `merge_to_main` — local sync.
- `story_close` — transition + reviewer verdict.

## How it's used

The orchestrator agent reads this prose when composing a per-story plan
and submits the plan via `satellites_orchestrator_submit_plan`. The
`story_reviewer` agent (Gemini-backed) checks the proposed contract
list against the mandate principle (`pr_mandate_reviewer_enforced`) and
either accepts or asks for revisions.

The orchestrator MAY add optional middle slots (e.g. an extra `develop`
for a multi-stage implementation), drop steps that don't apply to a
particular story, or amend mid-story via `satellites_plan_amend`. The
reviewer judges whether the proposed shape is appropriate; the
substrate accepts whatever the reviewer approves.

## Floor

The mandate principle requires `plan` at the front and `story_close`
at the end of every story. Everything else is the orchestrator's
choice. Adding a new mandatory contract is an edit to the principle
text and the reviewer agent rubrics, not a Go change.
