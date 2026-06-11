---
name: satellites-skill-authoring
type: skill
kind: capability
description: Author or revise a satellites skill as configuration — defines the target shape (Spec/Verifier/Environment) and the review gate. Invoke when creating or reworking a substrate skill; produce it into .satellites/skills/ and run satellites-skill-review before upload.
scope: system
tags: [kind:capability, area:substrate]
---
<!-- satellites-sync:begin {"document_id":"doc_d0124e6d","version":4,"hash":"fd3b174b51c7b13834172eddbee9a24c768e02314ca2d5474701f4b750a289fb"} satellites-sync:end -->
# satellites-skill-authoring

Author or revise a satellites `skill` as configuration. Use when creating a new skill or reworking an existing one for the substrate. The external `skill-creator` may help draft prose; this skill defines the satellites target shape and gate. Produce the refined skill into `.satellites/skills/<name>.md`, then run `satellites-skill-review` before upload.

## Clarify first — never draft blind

Before writing anything, interview the operator to surface the TRUE goal, not the surface task:

- What outcome must this skill produce, and when should an agent reach for it?
- What does "done/correct" look like — how will an agent know it worked?
- What must it never do; what must it always do; what needs a human's say-so?

Restate the goal back and get agreement. Only then draft.

## Draft to the three layers

A skill is built on Spec, Verifier, Environment — write each explicitly (kind-aware):

- **Spec** — the contract. For a `workflow`, the `## Workflow` states/transitions; for a `gate`, the decision rule; for a `capability`/`function`, the purpose + the clarified questions it answers. Imperative, minimal, repo-agnostic.
  **Atomic:** a `gate` carries exactly ONE decision rule with one verdict. A non-gate skill never embeds a fail-closed check or verdict routine — it NAMES the gate skill to run and honours its verdict; the routine has one home, the gate.
- **Verifier** — how success is checked, not assumed: a gate to run, eval criteria stated upfront, a test, or an external/measurable signal. Prefer verification over raw generation.
- **Environment** — the scope it runs in, plus a guardrail block:

  ```yaml
  guardrails:
    always: [ ... ]      # invariants to uphold every run
    ask_first: [ ... ]   # actions needing the operator's say-so
    never: [ ... ]       # hard limits the skill must not cross
  ```

  A pure read-only/advisory skill may declare no guardrails and say so.

## Then review + ship

Run `satellites-skill-review` and resolve every REVISE before upload. The skill is configuration — keep it terse, prescriptive, and free of concrete substrate ids. Declare the guardrails as part of the Environment; a runtime hook consumes them. Keep this skill to authoring.
