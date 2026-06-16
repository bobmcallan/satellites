---
name: satellites-doc-drift-review
type: skill
kind: gate
when: pre-commit
tags: [kind:gate, content-review:allow-refs]
description: Run before every commit that touches the CLI. The command-surface drift gate fails closed when a live `satellites` command is not named in the command-surface reference doc, so an update that adds, renames, or removes a command cannot ship while the docs read stale. The commit-push routine names it among its pre-commit gates.
---
<!-- satellites-sync:begin {"document_id":"doc_f4c3dfcb","version":3,"hash":"75646bdf4e004ee217811a19c66e8a75ed9808f47f378c5fb1c4456260e6daad","publisher":"proj_682cfeed"} satellites-sync:end -->
<!-- satellites-library:begin {"publisher":"proj_682cfeed","repo":"https://github.com/bobmcallan/satellites-skills","commit":"7caa10cbeb50ac1856b1576e7ffbdafc7ca746eb"} satellites-library:end -->

# satellites-doc-drift-review

Run before the commit step of every `satellites-commit-push` that touches the CLI
(`internal/cli`, `cmd/satellites`), and any time you have added, renamed, or
removed a command. It fails closed when a live command is absent from the
command-surface doc.

## Spec

The decision rule: every live top-level command must be named in the
`client-command-surface` document. If any live command is absent, the gate fails
closed and the commit must not proceed. The question it answers is "is every live
command documented?"

## Routine

Run the gate from the repo root:

```bash
.satellites/satellites surface check
```

It enumerates the live top-level commands (excluding cobra's auto-generated
`help`/`completion`), reads the `client-command-surface` document, and reconciles
them.

**Exit 0 (CLEAN)** — every live command is named in the doc. The commit may
proceed.

**Exit 1 (BLOCKED)** — a command exists in the binary but not in the doc. Do not
commit. Reconcile it in the same change:

1. Add the command to `.satellites/documents/client-command-surface.md` — name
   it, say what it owns, give its state.
2. `satellites document upload`, then re-run the gate.

The gate is the verifier: exit 0 = CLEAN, exit 1 = BLOCKED, and the loop is
re-run after reconciling until it returns CLEAN.

## Environment

Runs in the target satellites repo against the live binary and the
`client-command-surface` document. The reconcile step mutates external state —
`satellites document upload` publishes a document — so the gate is bounded by:

```yaml
guardrails:
  always:
    - Run `surface check` from the repo root against the live binary.
    - Reconcile a BLOCKED result in the same change that introduced the drift.
  ask_first:
    - Before `satellites document upload`, since it publishes the document.
  never:
    - Commit or push past a BLOCKED result.
    - Bypass the gate with `--skip-review` or any equivalent override.
```
