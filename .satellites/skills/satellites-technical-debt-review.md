---
name: satellites-technical-debt-review
type: skill
kind: gate
when: pre-commit
tags: [kind:gate, content-review:allow-refs]
description: The technical-debt gate (broken-windows enforcement). Runs as the techdebt-review state's command on the workflow's checkpoint traverse, against the local tree BEFORE anything ships — build + unit + the integration tier reconciled against the quarantine register, fail closed on any unregistered red. At commit the tree must be clean OR its debt must be a story.
---

# satellites-technical-debt-review

Runs as the `techdebt-review` STATE's command: the client executes it on the
workflow's checkpoint traverse, against the local working tree, BEFORE the ship
routine ([[satellites-commit-push]]) may run — a fail leaves the remote
untouched. Also run it any time you want to know the tree is clean before
staking a review on it. It enforces, at commit, that the tree is **clean OR its
debt is a story** — a found failure is yours to fix or file, never to pass by.

**Scope.** This gate is for satellites Go repositories: it drives the Go
toolchain (`go build` / `go test`) and the `.satellites/` layout (the CLI, the
register document). It does not apply to non-Go or non-satellites trees.

## Routine

Run the gate from the repo root:

```bash
.satellites/satellites techdebt review
```

It does, in order:

1. `go build ./...` — a broken build always blocks (never registerable).
2. `go test ./...` (unit) — a compile/setup failure here blocks.
3. The integration tier (`go test -tags integration ./tests/integration/...`).
   If Docker is up it runs; if it cannot start, the tier is **skipped with a
   warning** (not blocked). Pass `--skip-integration` to force-skip.
4. Reconciles the failing checks against the `technical-debt-register` document
   and prints a verdict.

This local gate is the **sole integration gate**. The `test` CI workflow runs
the UNIT/vet path only (release+deploy gate on it) — it does NOT re-run the
integration tier, because headless Chrome in a CI runner flakes on the
websocket handshake and costs runner minutes, while locally it is stable. So
the integration tier runs HERE, against the local tree, before every
commit/push; a red is fixed (the loop), never carried, and CI does not consult
this register. The tradeoff: if you skip the local tier (Docker down,
`--skip-integration`) there is no CI backstop, so integration is unverified for
that push — do not skip it before shipping a change with integration surface.

**Exit 0 (CLEAN)** — no new red and no unowned register entry. The commit may
proceed.

**Exit 1 (BLOCKED)** — a *new red* (a failing check the register does not name)
or an *unowned* register row. Do not commit. Resolve it:

- **Fix it** — the broken-windows default. Make the check green, re-run the gate.
- **Or file it** — when the failure genuinely cannot be fixed in this change:
  1. Create a tracking story (`document_upsert` type:story) naming the failing
     check and what must be done.
  2. Add an **owned** row to `.satellites/documents/technical-debt-register.md`:
     `| <check_id> | <story_id> | <reason> |`. Every row MUST name its story — an
     unowned row blocks.
  3. `satellites document upload`, then re-run the gate.

### Triage first: is the red *yours*?

Before sinking time into a fix, establish whether your in-flight change caused it.

1. **Confirm provenance.** Stash your change and re-run the failing check on the
   clean tree:

   ```bash
   git stash push -u -- cmd/ internal/ tests/
   go test -tags integration -run <CheckName> ./tests/integration/ -count=1
   git stash pop
   ```

   Always pair the `push` with its `pop` — the `-u` stash also lifts untracked
   files, so a skipped or failed `pop` strands work. If `pop` errors (e.g. a
   conflict), resolve it before doing anything else; never leave the stash on the
   stack.

   - **Still red without your change → pre-existing & unrelated.** Do NOT fix it
     inline. File it (above) in the *owning feature's epic* (the area whose code
     the check exercises), register the row, upload, re-run → proceed. Note the
     provenance in the story so the next executor doesn't re-litigate it.
   - **Green without your change → you caused it.** Fix it before committing. The
     register is not an escape hatch for your own regressions.

2. When unsure which epic owns it, `git log` the failing check's file to find the
   introducing change and its epic; file there.

## The register only shrinks

When the gate reports a check as **STALE** on a complete run (a registered check
that now passes), the window has closed — **remove that row** and let its story
close. The register grows only by deliberate, story-backed capture, and every
fixed window forces a removal.

## Environment

Runs from the repo root of a satellites Go checkout. The gate is read-only, but
its resolution path is not: filing debt mutates the working tree (stories, the
register) and publishes to an external store via `document upload`. The triage
step stashes and restores tracked + untracked code. Bound those actions:

```yaml
guardrails:
  always:
    - Run from the repo root; reconcile every failing check against the technical-debt-register.
    - Every register row names an owning story — an unowned row blocks.
    - Pair every `git stash push -u` with its `git stash pop`; never leave a stash on the stack or untracked files stranded.
    - Fail closed — Exit 1 on any new red or unowned row blocks the commit.
  ask_first:
    - Filing a pre-existing, unrelated red into another feature's epic rather than your own.
    - Removing a register row (STALE close-out) when you are not the row's owner.
  never:
    - Commit while the gate reports Exit 1 (new red or unowned row).
    - Register a broken build or a compile/setup failure — those are never registerable, only fixable.
    - Use the register to absorb a regression your own in-flight change introduced.
```
