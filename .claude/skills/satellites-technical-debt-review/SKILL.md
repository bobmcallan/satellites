<!-- satellites-sync:begin {"document_id":"doc_4732f444","version":4,"hash":"38a9d7a300d33af19abef65dcac10e532981530c9a5a70be0b0a7ba2a81ed673"} satellites-sync:end -->
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
  2. Add an **owned** row to `.satellites/documents/technical-debt-register.md`:
     `| <check_id> | <story_id> | <reason> |`. Every row MUST name its story —
     an unowned row blocks.
  3. `satellites document upload`, then re-run the gate. The now-registered red
     is absorbed.

### Triage first: is the red *yours*?

A new red is not automatically yours to fix. Before sinking time into a fix,
establish whether your in-flight change caused it — fixing someone else's
pre-existing breakage is scope creep into another subsystem and stalls the
story you are actually on.

1. **Confirm provenance.** Stash your change and re-run the failing check on the
   clean tree:

   ```bash
   git stash push -u -- cmd/ internal/ tests/   # set aside the in-flight change
   go test -tags integration -run <CheckName> ./tests/integration/ -count=1
   git stash pop
   ```

   - **Still red without your change → pre-existing & unrelated.** Do NOT fix it
     inline. **File it** (above): open a tracking story in the *owning feature's
     epic* (the epic/area whose code the check exercises, not the epic you happen
     to be on), register the row against that story, upload, re-run → proceed.
     Note the provenance ("fails on clean tree, unrelated to <your-story>") in
     the story so the next executor doesn't re-litigate it.
   - **Green without your change → you caused it.** It is yours: fix it before
     committing. The register is not an escape hatch for your own regressions.

2. When in doubt about which epic owns it, `git log` the failing check's file /
   feature to find the introducing change and its epic; file there.

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
