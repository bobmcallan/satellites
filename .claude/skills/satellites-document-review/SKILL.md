<!-- satellites-sync:begin {"document_id":"doc_be9b60e2","version":1,"hash":"f32bef8e00c002debd9ea4d95094c5cf049973e3f0ba9480f6ee97197fae5e32"} satellites-sync:end -->
---
name: document-review
type: skill
kind: capability
scope: system
tags: [kind:capability, area:substrate]
---
# document-review

Review a `document` artifact before it is uploaded to the substrate. A document is *consulted* — reference or intent prose a downstream agent or operator reads without your context. Run this review on the file you are about to `satellites document upload`, report findings, and fix them with the author before the upload. The CLI enforces the strict checks below as a hard block; this skill is the conversation that keeps the artifact maintainable.

## Strict checks — the CLI blocks these

- **No drift-prone references.** Reject a concrete substrate slug written into prose (`<prefix>_<hex>` forms such as a story id, document id, workspace id, or project id). These rot as the referenced row closes or renames. Use a template form (`story:<id>`, `document:<scope>/<name>`, `project:<id>`) or fold the fact into prose instead. The `satellites document upload` content gate fails on a concrete slug; `--skip-review` overrides only after this review.
- **Links resolve.** Every relative path named outside a code fence must exist, or be a `<placeholder>`.

## Maintainability critique — the conversation

Read the whole file, then answer each with PASS / REVISE and one sentence of evidence:

1. **Durable.** Will this still be true after the work it describes lands? Flag anything that pins to volatile state — a specific story id, a version pin, or implementation-status narrative.
2. **Environment-agnostic.** Could it ship unchanged to a reader who has never seen this repository? Flag host-repo coupling and source-tree paths.
3. **Consult, not instruct.** A document states load-bearing facts or intent; it does not carry a process (that is a skill) or an always-on rule (that is a principle). Flag misplaced content.
4. **Minimum length.** Could 20% of the words go without losing intent?

End with one verdict: SHIP / REVISE. Do not rewrite unless asked.
