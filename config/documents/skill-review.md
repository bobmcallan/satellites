---
name: skill-review
type: skill
kind: capability
scope: system
tags: [kind:capability, area:substrate]
---
# skill-review

Review a `skill` artifact before it is uploaded to the substrate. A skill is *followed* — a process or function an agent runs, carrying a yaml dispatch contract in its frontmatter. Run this review on the file you are about to `satellites skill upload`, report findings, and fix them with the author first. The CLI enforces the strict checks below as a hard block; this skill is the conversation that keeps the process clear and dispatchable.

## Strict checks — the CLI blocks these

- **No drift-prone references.** Reject a concrete substrate slug in prose (`<prefix>_<hex>` — a story, document, workspace, or project id). A skill is run repeatedly long after any one row; use a template form (`story:<id>`) or prose, never a live id. The upload content gate fails on a concrete slug; `--skip-review` overrides only after this review.
- **Links resolve.** Relative paths named outside a code fence must exist or be a `<placeholder>`.

## Maintainability critique — the conversation

Read the whole file, then answer each PASS / REVISE with one sentence of evidence:

1. **Dispatch contract present.** Does the frontmatter declare `kind` (workflow / function / gate / capability) and, for a workflow, `applies_to`? A skill the index cannot classify will not be selected.
2. **Triggerable description.** Does the `description` say plainly when to use the skill, so the index matches it to the right story type and status? Flag a vague or narrative description.
3. **Prescriptive.** Does the body open with and stay in imperative voice — directives the runner acts on, not background? Flag scene-setting.
4. **Followable + agnostic.** Could an agent with only this text and the surrounding tool surface run it without guessing, in any repository? Flag ambiguity and host-repo coupling.

End with one verdict: SHIP / REVISE. Do not rewrite unless asked.
