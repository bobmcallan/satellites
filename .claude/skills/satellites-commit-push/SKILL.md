<!-- satellites-sync:begin {"document_id":"doc_e12fad56","version":6,"hash":"c22f0822335997726ffd822f3dca72f6b8aa1849c8010556660beb83a8703149"} satellites-sync:end -->
---
name: satellites-commit-push
type: skill
kind: capability
when: checkpoint
tags: [kind:capability]
description: Commit and push satellites at a story checkpoint — bump .version, conventional commit (no AI attribution), push, and watch the CI chain (test → release → deploy). Run at every natural checkpoint and before requesting review, so the change is visible to reviewers and the build pipeline. The process-owned counterpart of the operator's /commit-push shadow.
---

# satellites-commit-push

Run at every natural checkpoint and before requesting review — reviewers judge the
latest pushed commit, not the local tree. A change not committed + pushed +
(where a binary changed) released is invisible to the gate.

**No AI attribution** in commit messages (no "Claude", "AI", "automated",
"assistant", "co-author"). Conventional commit format: `type(scope): description`.

## Routine

1. **Technical-debt gate — pre-commit, fail closed** ([[satellites-technical-debt-review]]):

   ```bash
   .satellites/satellites techdebt review
   ```

   **Exit 0 (CLEAN)** → proceed. **Exit 1 (BLOCKED)** → a new red or an unowned
   register row; **do not commit**. Fix it, or file a tracking story and add an
   owned register row (`| <check_id> | <story_id> | <reason> |`),
   `satellites document upload`, and re-run. A STALE row on a complete run means a
   window closed — remove it. "It was already broken" is not a pass.

1b. **Command-surface drift gate — pre-commit, fail closed** when the change
   touches the CLI ([[satellites-doc-drift-review]]):

   ```bash
   .satellites/satellites surface check
   ```

   **Exit 0 (CLEAN)** → proceed. **Exit 1 (BLOCKED)** → an added/renamed command
   is undocumented; reconcile the doc in this same change
   (`satellites document upload`) and re-run. Skip only when the change does not
   touch `internal/cli` / `cmd/satellites`.

2. **Configure + stage**

   ```bash
   git config user.name "bobmcallan" && git config core.autocrlf input && git add -A
   ```

3. **Analyse + format** — `git branch --show-current`, `git diff --cached --stat`,
   `git log --oneline -5` for style. If `go.mod` exists: `gofmt -s -w . && git add -u`.

4. **Bump `.version` — MANDATORY on every commit.** `.version` is per-binary:
   `satellites.version` = the **CLI**, `satellites-server.version` = the
   **server**. Bump the patch of the binary(ies) the change touches.

   The release gate's **CLIENT_PATHS** (`.github/workflows/release.yml`) are the
   authoritative definition of "client-affecting" → bump **`satellites.version`**:

   ```
   internal/cli/  cmd/satellites/  internal/verb/  internal/workstate/  internal/workflow/  internal/audit/
   ```

   - **CLI / client** (any CLIENT_PATH above) → bump `satellites.version`.
     `internal/verb` IS a client path (compiled into the CLI) — a verb change needs
     a `satellites.version` bump, not just a server bump.
   - **server-only** (`internal/server`, `internal/mcpserver`,
     `internal/document`, `cmd/satellites-server`, other non-client packages) →
     bump `satellites-server.version`.
   - **both** — code compiled into BOTH binaries bumps both versions:
     `config/documents/` embedded seeds, and `internal/verb`. When in doubt, bump both.

   Update the matching `*.build` timestamp. `git add .version`. Never skip — the
   release tag derives from `satellites.version`, and a CLIENT_PATH change with no
   bump FAILS the release workflow.

5. **Commit, gate the release locally, then push.**

   ```bash
   git commit -m "type(scope): message"
   .satellites/satellites release check
   git push
   ```

   `release check` mirrors the CI release gate locally: if a CLIENT_PATH changed
   since the released `v<satellites.version>` tag and `satellites.version` was not
   bumped, it **BLOCKS** — go back to step 4, bump it (`git commit --amend` or a
   follow-up commit), and re-run.

6. **Watch CI** (`.github/workflows/`: test → release → deploy) — three
   **separate** workflows. Check ALL THREE conclusions, especially **release**: it
   does NOT silently skip — it FAILS when a CLIENT_PATH changed without a
   `satellites.version` bump, and that red is invisible if you only watch `test` +
   `deploy`.

   ```bash
   HEAD=$(git rev-parse HEAD)
   gh run list --commit "$HEAD" --json workflowName,status,conclusion --jq '.[] | "\(.workflowName): \(.status) \(.conclusion)"'
   gh run watch <test-run-id> --exit-status
   ```

   On failure, surface the failing step (`gh run view <id> --log-failed`) and stop
   — do not amend or retry unless asked. On success, report the runs + the release tag.

7. **Record the CI outcomes into the QA-evidence trail.** Once the three workflows
   have concluded, capture each stage:

   ```bash
   scripts/record-ci-evidence.sh   # story id from HEAD's commit trailer; idempotent
   ```

   It writes a `ci_result` row per stage (test/release/deploy) via
   `satellites evidence ci`, keyed to the story in the commit trailer. Confirm with
   `satellites evidence show <story>`.
