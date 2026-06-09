---
name: satellites-doc-drift-review
type: skill
kind: capability
when: pre-commit
tags: [kind:capability, content-review:allow-refs]
description: The command-surface drift gate. Run before every commit that touches the CLI — it fails closed when a live `satellites` command is not named in the command-surface reference doc, so an update that adds, renames, or removes a command cannot ship while the docs read stale. The commit-push routine names it alongside the technical-debt gate.
---

# satellites-doc-drift-review

Run before the commit step of every `satellites-commit-push` that touches the CLI
(`internal/cli`, `cmd/satellites`), and any time you have added, renamed, or
removed a command. It fails closed when a live command is absent from the
command-surface doc.

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
