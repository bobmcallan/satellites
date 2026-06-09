<!-- satellites-sync:begin {"document_id":"doc_be9b60e2","version":2,"hash":"9dc4247143077233de1796027cdf3d0583075112ae9945b249ed26f6db4835c3"} satellites-sync:end -->
---
name: document-review
type: skill
kind: capability
scope: system
tags: [kind:capability, area:substrate]
---
# document-review

Review a `document` file before `satellites document upload`. Report findings and fix them with the author. The CLI hard-blocks the strict checks below; this review keeps the artifact maintainable.

## Strict checks — the CLI blocks these

- **No drift-prone references.** Reject a concrete substrate slug in prose (`<prefix>_<hex>` — a story, document, workspace, or project id). Use a template form (`story:<id>`, `document:<scope>/<name>`, `project:<id>`) or fold the fact into prose. The `satellites document upload` content gate fails on a concrete slug; `--skip-review` overrides only after this review.
- **Links resolve.** Every relative path named outside a code fence must exist, or be a `<placeholder>`.

## Maintainability critique

Read the whole file, then answer each PASS / REVISE with one sentence of evidence:

1. **Durable.** Will this still be true after the work it describes lands? Flag a specific story id, a version pin, or implementation-status narrative.
2. **Environment-agnostic.** Could it ship unchanged to a reader who has never seen this repository? Flag host-repo coupling and source-tree paths.
3. **Consult, not instruct.** A document states facts or intent; it does not carry a process (a skill) or an always-on rule (a principle). Flag misplaced content.
4. **Minimum length.** Could 20% of the words go without losing intent?

End with one verdict: SHIP / REVISE. Do not rewrite unless asked.
