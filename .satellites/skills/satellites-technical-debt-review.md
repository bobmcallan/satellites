---
name: satellites-technical-debt-review
type: skill
kind: capability
when: pre-commit
tags: [kind:capability, content-review:allow-refs]
description: The technical-debt pre-commit gate (broken-windows enforcement). Run before every commit — build + unit + the integration tier reconciled against the quarantine register, fail closed on any unregistered red. The commit-push routine names it; at commit the tree must be clean OR its debt must be a story.
---

# satellites-technical-debt-review

Run before the commit step of every `satellites-commit-push` (it is the wired-in
pre-commit gate), or any time you want to know the tree is clean before staking a
review on it. It enforces, at commit, that the tree is **clean OR its debt is a
story** — a found failure is yours to fix or file, never to pass by.

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
