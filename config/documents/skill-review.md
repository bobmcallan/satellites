---
name: skill-review
type: skill
kind: capability
scope: system
tags: [kind:capability, area:substrate]
---
# skill-review

Review a `skill` file before `satellites skill upload`. Report findings and fix them with the author. The CLI hard-blocks the strict checks below; this review keeps the process dispatchable.

## Strict checks — the CLI blocks these

- **No drift-prone references.** Reject a concrete substrate slug in prose (`<prefix>_<hex>` — a story, document, workspace, or project id). Use a template form (`story:<id>`) or prose, never a live id. The upload content gate fails on a concrete slug; `--skip-review` overrides only after this review.
- **Links resolve.** Relative paths named outside a code fence must exist or be a `<placeholder>`.

## Maintainability critique

Read the whole file, then answer each PASS / REVISE with one sentence of evidence:

1. **Dispatch contract present.** Does the frontmatter declare `kind` (workflow / function / gate / capability) and, for a workflow, `applies_to`?
2. **Triggerable description.** Does the `description` say plainly when to use the skill? Flag a vague or narrative description.
3. **Prescriptive.** Does the body open with and stay in imperative voice? Flag scene-setting.
4. **Followable + agnostic.** Could an agent run it without guessing, in any repository? Flag ambiguity and host-repo coupling.

End with one verdict: SHIP / REVISE. Do not rewrite unless asked.
