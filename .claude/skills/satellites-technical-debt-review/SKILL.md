<!-- satellites-sync:begin {"document_id":"doc_4732f444","version":2,"hash":"759f27833b21b2e6205574efb834f8a5586b8808b1c8f89eedcf313aba077eda"} satellites-sync:end -->
---
name: satellites-technical-debt-review
type: skill
kind: capability
when: pre-commit
tags: [kind:capability, content-review:allow-refs]
description: The technical-debt pre-commit gate (broken-windows enforcement). Run before every commit — build + unit + the integration tier reconciled against the quarantine register, fail closed on any unregistered red. The commit-push routine names it; at commit the tree must be clean OR its debt must be a story.
---

# satellites-technical-debt-review

The enforcement half of the broken-windows programme ([[broken-windows]],
sty_dd128ef6). It runs the verification the done-review gate cannot — build +
unit + the **integration tier** (which runs in no other gate) — and enforces, at
commit, that the tree is **clean OR its debt is a story**.

It is the [[principle:broken-windows]] rule made non-optional: a found failure
is yours to fix or file, never to pass by.

## When to run

Before the commit step of every `satellites-commit-push` (it is wired in as the
pre-commit gate — see that skill). Also run it any time you want to know whether
the tree is clean before you stake a review on it.

## Routine

Run the gate from the repo root:

```bash
.satellites/satellites techdebt review
```

It does, in order:

1. `go build ./...` — a broken build always blocks (it is never registerable).
2. `go test ./...` (unit) — a compile/setup failure here blocks.
3. The integration tier (`go test -tags integration ./tests/integration/...`).
   If Docker is up it runs; if it cannot start, the tier is **skipped with a
   warning** (not blocked). Pass `--skip-integration` to force-skip.
4. Reconciles the failing checks against the `technical-debt-register`
   document and prints a verdict.

**Exit 0 (CLEAN)** — no new red and no unowned register entry. The commit may
proceed.

**Exit 1 (BLOCKED)** — there is a *new red* (a failing check the register does
not name) or an *unowned* register row. Do not commit. Resolve it:

- **Fix it** — the broken-windows default. Make the check green, re-run the gate.
- **Or file it** — when the failure genuinely cannot be fixed in this change:
  1. Create a tracking story (`document_upsert` type:story) that names the
     failing check and what must be done.
  2. Add an **owned** row to `config/<wksp>/<proj>/documents/technical-debt-register.md`:
     `| <check_id> | <story_id> | <reason> |`. Every row MUST name its story —
     an unowned row blocks.
  3. `satellites document upload`, then re-run the gate. The now-registered red
     is absorbed.

## The register only shrinks

When the gate reports a check as **STALE** on a complete run (a registered check
that now passes), the window has closed — **remove that row** from the register
and let its story close. The register trends down: it grows only by a deliberate,
story-backed capture, and every fixed window forces a removal. See
[[broken-windows]].

## Why a CLI subcommand, not a new verb

The reconcile is the pure `internal/techdebt` core; the register is read with
the existing `document_get` verb; the gate is a client-side CLI subcommand. No
new MCP verb — client ergonomics ship as `satellites` subcommands
([[no-new-mcp-verbs]]). The register is data, the gate is configuration the
commit-push routine names ([[principle-configuration-over-code]]).
