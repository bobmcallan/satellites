---
description: Git commit and push for satellites with full CI chain monitoring (test → release → deploy). Shadows the global /commit-push so the deploy chain is always watched in this repo. No AI attribution.
---

**INSTRUCTION:** Execute commit and push directly (no subagent).

**CRITICAL RULES:**
- NO AI attribution (Claude, AI, automated, assistant, co-author)
- Conventional commit format: `type(scope): description`
- AUTO-CONFIRM all git operations

**CI topology (this repo):**

```
push → test (push trigger)
         └─[success]─► release (workflow_run)
         └─[success]─► deploy  (workflow_run)
```

`release.yml` and `deploy.yml` both trigger on `workflow_run` after
`test` completes successfully. Monitoring only the `test` run misses
the deploy outcome — the previous global skill stopped too early.
This skill watches all three.

**Workflow:**

1. **Setup & Stage** (single chained command):
   ```bash
   git config user.name "bobmcallan" && git config --global credential.github.com.username bobmcallan && git config core.autocrlf input && git add .
   ```

2. **Analyze** (parallel calls):
   - `git branch --show-current`
   - `git diff --cached --stat`
   - `git log --oneline -5` (for commit style reference)
   - **Format** — `gofmt -s -w . && git add -u` (this repo is Go).

3. **Bump version** — MANDATORY on every commit. `.version` carries
   two independent per-binary versions:

   ```
   satellites.version: <CLI>
   satellites.build:   <UTC>
   satellites-server.version: <server>
   satellites-server.build:   <UTC>
   ```

   Pick which to bump from the staged paths:

   | Staged paths include…                                                       | Bump                          |
   | --------------------------------------------------------------------------- | ----------------------------- |
   | `cmd/satellites/`, `internal/cli/`, `internal/cliconfig/`                   | `satellites.*`                |
   | `cmd/satellites-server/`, `internal/server/`, `internal/server/static/`, `internal/server/templates/` | `satellites-server.*` |
   | Shared (`internal/verb/`, `internal/document/`, `internal/project/`, `internal/workspace/`, `internal/ledger/`, `internal/variable/`, `internal/auth/`, `internal/reviewer/`, `internal/arbor/`, `internal/db/`, `internal/mcpserver/`, `internal/config/`) | Both                          |
   | Tests / docs / CI / scripts only                                            | `satellites-server.*` (so deploy ships) |

   - Increment patch (`0.0.67` → `0.0.68`).
   - Set the matching `*.build` to `date -u +"%Y-%m-%d-%H-%M-%S"`.
   - `git add .version`.
   - Release tag is `v<satellites.version>`; release-job's
     `needs_release` check skips when neither bumped, so a missed bump
     means no tag is cut. Deploy still rolls out the image. Bump
     `satellites-server.*` whenever you want the running deploy
     refreshed.

4. **Commit & Push** (single chained command):
   ```bash
   git commit -m "type(scope): message" && git push
   ```

   Capture the pushed SHA for Step 5:
   ```bash
   SHA=$(git rev-parse HEAD)
   ```

5. **Monitor the full chain** — three workflows must complete. Do
   not stop after `test`.

   **5a. Find and watch the test run for the pushed SHA:**
   ```bash
   TEST_ID=$(gh run list --branch main --workflow test --limit 5 \
     --json databaseId,headSha,status \
     -q ".[] | select(.headSha==\"$SHA\") | .databaseId" | head -1)
   gh run watch "$TEST_ID" --exit-status
   ```
   - If `gh run watch` exits non-zero: `gh run view "$TEST_ID" --log-failed`, surface the failing step, stop. `release` and `deploy` are gated on test success — they will not run.

   **5b. Wait for release + deploy runs to appear.** They are
   `workflow_run`-triggered, so they spawn only after `test`
   finishes. Poll briefly (they typically appear within ~10s):
   ```bash
   for i in $(seq 1 20); do
     RELEASE_ID=$(gh run list --branch main --workflow release --limit 5 \
       --json databaseId,headSha,status \
       -q ".[] | select(.headSha==\"$SHA\") | .databaseId" | head -1)
     DEPLOY_ID=$(gh run list --branch main --workflow deploy --limit 5 \
       --json databaseId,headSha,status \
       -q ".[] | select(.headSha==\"$SHA\") | .databaseId" | head -1)
     [ -n "$RELEASE_ID" ] && [ -n "$DEPLOY_ID" ] && break
     sleep 3
   done
   ```
   If either id is still empty after the loop, report which one
   didn't spawn (likely a workflow trigger config issue) and stop.

   **5c. Watch release + deploy.** Use `run_in_background` to start
   both `gh run watch` invocations concurrently, then surface each
   result as it returns:
   ```bash
   gh run watch "$RELEASE_ID" --exit-status   # run in background
   gh run watch "$DEPLOY_ID"  --exit-status   # run in background
   ```
   On any failure: `gh run view "<failing_id>" --log-failed`, surface
   the failing step, stop.

   **Do NOT retry, push fixes, or amend on failure unless the user
   asks.** Surface the failure and stop.

**Commit Types:** feat | fix | docs | refactor | test | chore

**Output:** Commit hash, message, push status, then the conclusion
of each of the three runs (`test`, `release`, `deploy`) with their
run URLs.

**Context:** $ARGUMENTS
