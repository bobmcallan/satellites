---
name: principle-review
type: skill
kind: capability
scope: system
tags: [kind:capability, area:substrate]
---
# principle-review

Review a `principle` file before `satellites principle upload`. Report findings and fix them with the author. The CLI hard-blocks the strict checks below; this review keeps the rule durable.

## Strict checks — the CLI blocks these

- **No drift-prone references.** Reject a concrete substrate slug in prose (`<prefix>_<hex>` — a story, document, workspace, or project id). Reference durable concepts or a template form (`story:<id>`), never a live id. The upload content gate fails on a concrete slug; `--skip-review` overrides only after this review.
- **Links resolve.** Relative paths named outside a code fence must exist or be a `<placeholder>`.

## Maintainability critique

Read the whole file, then answer each PASS / REVISE with one sentence of evidence:

1. **One obeyable rule.** Does it state a single constraint an agent can obey on every relevant action? Flag a principle that bundles several rules or reads as guidance.
2. **Always-on, not situational.** A principle applies continuously; a one-time procedure is a skill and a fact is a document. Flag misplaced content.
3. **Testable.** Could a reviewer point at an action and say "this violates it"? Flag a rule too vague to enforce.
4. **Durable + agnostic.** Will it hold after current work lands, and read cleanly to someone outside this repository? Flag in-flight pins and host-repo coupling.
5. **Not skill-shaped.** A principle is a belief, not a procedure — flag any `Spec` / `Verifier` / `Environment` scaffolding, numbered steps, or guardrail block. Those belong in a skill; here they mean the content is misclassified.
6. **Residency tag intentional.** If the file carries `principles:always` it is injected into every session — flag it unless the belief genuinely must stay resident; if it lacks the tag, confirm it is fine to leave discoverable via the index. The tag is a deliberate choice, not a default.

End with one verdict: SHIP / REVISE. Do not rewrite unless asked.
