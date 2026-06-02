<!-- satellites-sync:begin {"document_id":"doc_e12fad56","version":2,"hash":"d580cbedb00cba9bf89e54ceb4056f1e84dc1f7478f9ed75b780afa404a9eca6"} satellites-sync:end -->
---
name: satellites-commit-push
type: skill
kind: capability
when: checkpoint
tags: [kind:capability]
description: Commit and push satellites at a story checkpoint — bump .version, conventional commit (no AI attribution), push, and watch the CI chain (test → release → deploy). Run at every natural checkpoint and before requesting review, so the change is visible to reviewers and the build pipeline. The process-owned counterpart of the operator's /commit-push shadow.
---

# satellites-commit-push

The process-owned commit + push checkpoint for **this repo**. It is a
`capability` the executor runs — not a reviewer gate. Project-scoped: the
`.version` bump and the Fly CI chain are satellites' own; a consumer project
supplies its own checkpoint skill.

**No AI attribution** in commit messages (no "Claude", "AI", "automated",
"assistant", "co-author"). Conventional commit format: `type(scope): description`.

## Routine

1. **Technical-debt gate — pre-commit, fail closed** ([[satellites-technical-debt-review]],
   [[broken-windows]]). Before anything else, run the gate:

   ```bash
   .satellites/satellites techdebt review
   ```

   It runs build + unit + the integration tier and reconciles the failing checks
   against the `technical-debt-register`. **Exit 0 (CLEAN)** → proceed. **Exit 1
   (BLOCKED)** → there is a new red (or an unowned register row); **do not
   commit**. Fix it, or file a tracking story and add an owned register row
   (`| <check_id> | <story_id> | <reason> |`), `satellites document upload`, and
   re-run. A STALE row on a complete run means a window closed — remove it (the
   register only shrinks). "It was already broken" is not a pass.

2. **Configure + stage**

   ```bash
   git config user.name "bobmcallan" && git config core.autocrlf input && git add -A
   ```

3. **Analyse + format** — `git branch --show-current`, `git diff --cached --stat`,
   `git log --oneline -5` for style. If `go.mod` exists: `gofmt -s -w . && git add -u`.

4. **Bump `.version` — MANDATORY on every commit.** `.version` is per-binary
   (`satellites.version` / `satellites-server.version`). Bump the patch of the
   binary(ies) the change touches — **CLI** (`internal/cli`, `cmd/satellites`),
   **server** (`internal/{verb,server,mcpserver,document,…}`,
   `cmd/satellites-server`, `config/documents/` embedded seeds), or both
   (`config/documents/` is embedded in both). Update the matching `*.build`
   timestamp. `git add .version`. Never skip — the release tag derives from
   `satellites.version`.

5. **Commit + push**

   ```bash
   git commit -m "type(scope): message" && git push
   ```

6. **Watch CI** (`.github/workflows/`: test → release → deploy). `test` runs on
   push; `release` tags `v<satellites.version>` (skips if the tag exists);
   `deploy` runs on `test` success and redeploys the server.

   ```bash
   gh run list --commit "$(git rev-parse HEAD)" --workflow test --json databaseId --jq '.[0].databaseId'
   gh run watch <id> --exit-status
   ```

   On failure, surface the failing step (`gh run view <id> --log-failed`) and
   stop — do not amend or retry unless asked. On success, report the run + the
   release tag.

## Why it is a checkpoint

Reviewers judge the latest pushed commit, not the local tree. A change that is
not committed + pushed + (where a binary changed) released and the local
`.satellites/satellites` refreshed is invisible to the gate — the most common
cause of a false reject. See [[commit-push-after-each-story]],
[[story-execution-process]].
