---
name: satellites-skill-review
type: skill
kind: capability
description: Review a skill file before `satellites skill upload` — strict drift/link checks the CLI hard-blocks, plus a kind-aware maintainability critique (Spec/Verifier/Environment). Invoke after authoring or revising a skill, before uploading it.
scope: system
tags: [kind:capability, area:substrate]
---
# satellites-skill-review

Review a `skill` file before `satellites skill upload`. Report findings and fix them with the author. The CLI hard-blocks the strict checks below; this review keeps the process dispatchable.

## Strict checks — the CLI blocks these

- **No drift-prone references.** Reject a concrete substrate slug in prose (`<prefix>_<hex>` — a story, document, workspace, or project id). Use a template form (`story:<id>`) or prose, never a live id. The upload content gate fails on a concrete slug; `--skip-review` overrides only after this review.
- **Links resolve.** Relative paths named outside a code fence must exist or be a `<placeholder>`.

## Maintainability critique

Read the whole file, then answer each PASS / REVISE with one sentence of evidence:

1. **Dispatch contract present.** Does the frontmatter declare `kind` (workflow / function / gate / capability) and, for a workflow, `applies_to`?
2. **Triggerable description.** Does the `description` say plainly when to use the skill? Flag a vague or narrative description.
3. **Prescriptive.** Does the body open with and stay in imperative voice? Flag scene-setting.
4. **Followable + scope-true.** Could an agent run it without guessing? Judge coupling BY SCOPE: a **system-scope** skill referencing repo-dev specifics — repo paths, CI workflow names, deploy hosts, version files, a host toolchain — is a blocking REVISE (system scope must work in any repository; the product's own install/distribution contract is not host coupling). A **project-scope** skill may be host-coupled by design — its repo IS its scope; flag only ambiguity there, not coupling.
5. **Spec — the contract is explicit.** Is the skill's goal + scope stated, not implied? Read it KIND-AWARE: a `workflow`'s spec IS its `## Workflow` states/transitions; a `gate`'s IS its decision rule; a `capability`/`function` needs an explicit purpose + the question its use answers. Flag a skill whose true goal an executor would have to guess.
6. **Verifier — success is checked, not assumed.** Does the skill say how an executor KNOWS it worked — a gate to run, eval criteria stated upfront, a test, or an external/measurable signal — rather than trusting raw output? A `gate` IS the verifier (judge its decision rule). Flag generation with no verification path.
7. **Environment + guardrails.** Does the skill declare its scope and an `always` / `ask-first` / `never` guardrail block bounding its tool use? Flag missing guardrails on a skill that acts on the repo or external services. (A pure read-only/advisory skill may state "no guardrails needed" and pass.)
8. **Atomic.** A `gate` skill states exactly ONE decision rule with one verdict. A non-gate skill must NOT embed a fail-closed check or verdict routine — it may only NAME a gate skill to run and honour its verdict. A composite that restates a gate's routine inline creates a second home that drifts. Violation = REVISE, always.

End with one verdict: SHIP / REVISE. Do not rewrite unless asked.

Items 5–7 are kind-aware and advisory (this critique, not the CLI hard-block): name any missing applicable layer in a REVISE; do not demand a `## Workflow` of a capability, and do not block a skill that already encodes the layer in its own form. Item 8 is NOT advisory — an atomicity violation is always a REVISE.
