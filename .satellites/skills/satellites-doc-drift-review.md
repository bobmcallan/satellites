---
name: satellites-doc-drift-review
type: skill
kind: capability
when: pre-commit
tags: [kind:capability, content-review:allow-refs]
description: The command-surface drift gate. Run before every commit that touches the CLI — it fails closed when a live `satellites` command is not named in the command-surface reference doc, so an update that adds, renames, or removes a command cannot ship while the docs read stale. The commit-push routine names it alongside the technical-debt gate.
---

# satellites-doc-drift-review

The doc half of the broken-windows programme for the **client surface**
([[broken-windows]], sty_d7698c22). The reference docs that describe what
`satellites <command>` you can run are only useful if they keep step with the
binary. When `update` ships a command change, the help/reference surface silently
drifts and the operator reads stale guidance. This gate makes that
non-optional: a live command absent from the command-surface doc blocks the
commit.

It is the companion of [[satellites-technical-debt-review]] — that gate keeps the
tree green, this one keeps the surface docs true.

## When to run

Before the commit step of every [[satellites-commit-push]] that touches the CLI
(`internal/cli`, `cmd/satellites`) — it is wired in as a second pre-commit gate.
Also run it any time you have added, renamed, or removed a command.

## Routine

Run the gate from the repo root:

```bash
.satellites/satellites surface check
```

It enumerates the live top-level commands (excluding cobra's auto-generated
`help`/`completion`), reads the `client-command-surface` document with the
existing `document_get` verb, and reconciles them.

**Exit 0 (CLEAN)** — every live command is named in the doc. The commit may
proceed.

**Exit 1 (BLOCKED)** — a command exists in the binary but not in the doc. Do not
commit. Reconcile it:

1. Add the command to `.satellites/documents/client-command-surface.md`
   — name it, say what it owns, give its state.
2. `satellites document upload`, then re-run the gate.

The doc and the binary ship in the same change, so the surface never drifts.

## Why a CLI subcommand, not a new verb

The drift check is the pure `surfaceDrift` core; the surface doc is read with the
existing `document_get` verb; the gate is a client-side CLI subcommand. No new
MCP verb — client ergonomics ship as `satellites` subcommands
([[no-new-mcp-verbs]]). The doc is data, the gate is configuration the
commit-push routine names ([[principle-configuration-over-code]]).
