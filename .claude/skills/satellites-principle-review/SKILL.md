<!-- satellites-sync:begin {"document_id":"doc_1b3ca969","version":1,"hash":"6fb550b3627cd43c899f4837db36b0a2d0c591bfbd6a257e6e577ac0c89c0735"} satellites-sync:end -->
---
name: principle-review
type: skill
kind: capability
scope: system
tags: [kind:capability, area:substrate]
---
# principle-review

Review a `principle` artifact before it is uploaded to the substrate. A principle is *obeyed* — an always-on constraint loaded into every applicable agent's context. Run this review on the file you are about to `satellites principle upload`, report findings, and fix them with the author first. The CLI enforces the strict checks below as a hard block; this skill is the conversation that keeps the rule durable.

## Strict checks — the CLI blocks these

- **No drift-prone references.** Reject a concrete substrate slug in prose (`<prefix>_<hex>` — a story, document, workspace, or project id). A principle outlives any single row; reference durable concepts or a template form (`story:<id>`), never a live id. The upload content gate fails on a concrete slug; `--skip-review` overrides only after this review.
- **Links resolve.** Relative paths named outside a code fence must exist or be a `<placeholder>`.

## Maintainability critique — the conversation

Read the whole file, then answer each PASS / REVISE with one sentence of evidence:

1. **One obeyable rule.** Does it state a single constraint an agent can obey on every relevant action? Flag a principle that bundles several rules or reads as guidance rather than a constraint.
2. **Always-on, not situational.** A principle applies continuously; a one-time procedure is a skill and a fact is a document. Flag misplaced content.
3. **Testable.** Could a reviewer point at an action and say "this violates it"? Flag a rule too vague to enforce.
4. **Durable + agnostic.** Will it hold after current work lands, and read cleanly to someone outside this repository? Flag in-flight pins and host-repo coupling.

End with one verdict: SHIP / REVISE. Do not rewrite unless asked.
