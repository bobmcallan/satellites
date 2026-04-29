---
id: pr_mandate_reviewer_enforced
name: Mandate enforced by reviewer, not substrate
scope: system
tags:
  - process
  - mandate
  - reviewer
  - v4
---
Every story must flow `preplan → plan → orchestrator-composed contracts → story_close`. `preplan` and `plan` are mandatory at the front; `story_close` is mandatory at the end; the orchestrator chooses the contracts that run in between. The reviewer agent enforces this floor; the substrate does not.

## Enforcement

The mandate is enforced in two places, both reviewer-driven:

- **Plan-approval loop.** When the orchestrator submits its proposed plan via `satellites_orchestrator_submit_plan`, the `story_reviewer` agent (Gemini-backed) checks that the proposed contract list begins with `preplan + plan` and ends with `story_close`. A plan missing the floor is rejected with `needs_more` and the orchestrator revises and resubmits. The loop is bounded by a KV-configurable cap (`plan_review_max_iterations`, default 5).

- **Per-contract close loop.** When each contract closes, the appropriate reviewer (`development_reviewer` for `develop`; `story_reviewer` for everything else) reads the evidence against the contract's rubric and either accepts or returns `needs_more`. The mandate is implicit at this layer — the reviewer cannot accept a `story_close` for a story that never had a `preplan`/`plan` because there is no evidence chain to point at.

The substrate has no `mandatory_slot_missing` gate, no `required_slots` resolver, no per-tier workflow merge. Those surfaces were removed by `epic:configuration-over-code-mandate` story_af79cf95 in favour of this principle and the reviewer agents that cite it.

## Why configuration, not code

A Go-coded mandate (resolved across system / workspace / project / user tiers and enforced at `workflow_claim` time) duplicates the orchestrator's job. The orchestrator's purpose is to compose the contract sequence per story; a substrate gate that pre-decides part of the sequence can — and at runtime did — disagree with the orchestrator's choice, with no recovery path that does not bypass one of the two layers.

Moving the mandate into a principle the reviewer cites means: adding a new mandatory contract is a configuration edit (this principle's text + the reviewer agent's rubric), not a Go change to a slot-algebra package.

## How to apply

- **Orchestrator.** When composing a plan, ensure `preplan` and `plan` lead the contract list and `story_close` ends it. Other contracts (`develop`, `push`, `merge_to_main`, project-defined slots) are the orchestrator's choice based on the story's shape. Submit the plan via `satellites_orchestrator_submit_plan` and loop on `needs_more` until accepted; on iteration-cap exceeded, escalate to the user.

- **Reviewer agents.** Cite this principle (`pr_mandate_reviewer_enforced`) when rejecting a plan that omits the floor. On per-contract closes, treat the mandate as a precondition — the chain of accepted CIs is what makes the closing review valid.

- **Authors of new contracts.** A new mandatory contract is added by editing this principle and the reviewer agent rubrics, not by changing Go code. A new optional contract needs no principle change — the orchestrator can include it whenever the story warrants.

## Citation

This principle backs `epic:configuration-over-code-mandate`. See `docs/architecture-configuration-over-code-mandate.md` (story_4362afb7) for the full design. Story story_af79cf95 removes the substrate enforcement surface; story story_6d259b99 seeds the reviewer agents that cite this principle; story story_0932c700 rewrites the orchestrator agent body to match.
