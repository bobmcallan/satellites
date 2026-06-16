---
name: satellites-commit-push
type: skill
kind: capability
when: checkpoint
tags: [kind:capability]
description: Commit and push satellites at a story checkpoint — bump .version, conventional commit (no AI attribution), push, and watch the CI chain (test → release → deploy). Run at every natural checkpoint and before requesting review, so the change is visible to reviewers and the build pipeline. The process-owned counterpart of the operator's /commit-push shadow.
---
<!-- satellites-sync:begin {"document_id":"doc_bfea47b2","version":3,"hash":"51c513fa96526eb71598745c18f3bf7d73fb9b4cb04438e2e1d6698bb6f5ba5f","publisher":"proj_682cfeed"} satellites-sync:end -->
<!-- satellites-library:begin {"publisher":"proj_682cfeed","repo":"https://github.com/bobmcallan/satellites-skills","commit":"7caa10cbeb50ac1856b1576e7ffbdafc7ca746eb"} satellites-library:end -->

# satellites-commit-push

Run at every natural checkpoint and before requesting review — reviewers judge the
latest pushed commit, not the local tree. A change not committed + pushed +
(where a binary changed) released is invisible to the gate.

**No AI attribution** in commit messages (no "Claude", "AI", "automated",
"assistant", "co-author"). Conventional commit format: `type(scope): description`.

## Routine

1. **Precondition — the techdebt-review traverse has already PASSED at this
   checkpoint.** The technical-debt verdict
   ([[satellites-technical-debt-review]]) is rendered by the workflow's
   `techdebt-review` state against the local tree BEFORE this capability runs
   — never ship from a tree whose traverse failed; on a fail, resolve per the
   gate skill and re-traverse before returning here.

   Then run the remaining pre-commit gates — each is its own atomic skill; this
   checkpoint only names them and honours their verdicts. A gate's routine,
   repair semantics, and guardrails live in the gate skill — the single home;
   do not restate or improvise them here.

   - **[[satellites-doc-drift-review]]** — when the change touches the CLI
     (`internal/cli`, `cmd/satellites`): `satellites surface check`. Exit 0 →
     proceed; exit 1 → **do not commit**, resolve per the gate skill, re-run.
   - **[[satellites-global-button-style-review]]** — when the change touches
     the portal UI (`internal/server/templates`, `internal/server/static`).
   - **[[satellites-workflow-drift-review]]** — when the change touches process
     configuration (skills, principles, workflows, story workflows):
     `satellites workflow check`. Exit 0 → proceed; exit 1 → **do not commit**,
     resolve per the gate skill, re-run.
   - **[[satellites-agent-architecture-review]]** — when the change touches the
     agent/executor surface (`internal/agent`, the agent executor in
     `internal/verb`, agent operating documents): a judgment gate (not a CLI
     check) — `satellites story status_transition --skill
     satellites-agent-architecture-review <story-id>`. It critiques the change
     for configuration-over-code (agent behaviour in the substrate, only
     mechanism in code). accept → proceed; reject → **do not commit**, move the
     flagged behaviour into the substrate, re-run.

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
   `deploy`. The **`test`** workflow now runs TWO parallel jobs — `vet-test`
   (unit) and `integration` (the full `-tags integration` tier, incl. chromedp);
   either job red fails `test` and blocks release+deploy, so CI is the
   non-skippable backstop to the local [[satellites-technical-debt-review]] gate.
   When `test` is red, `gh run view <id> --log-failed` shows which job.

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
   satellites evidence ci --from-head   # story id from HEAD's commit trailer; idempotent
   ```

   It writes a `ci_result` row per concluded stage (test/release/deploy), keyed
   to the story in the commit trailer; a stage with no concluded run is skipped.
   Confirm with `satellites evidence show <story>`.

## Environment

Runs inside the satellites repo working tree against its checked-out remote. It is
host-coupled by design: it relies on `.satellites/satellites`, the per-binary
`.version` files, and the release workflow's CLIENT_PATHS — it is not meant to run
in an arbitrary repository. It pushes to the remote and thereby triggers the
deploy workflow, so the bounds below apply to every run.

```yaml
guardrails:
  always:
    - Run only after the techdebt-review traverse has PASSED at this checkpoint — its verdict precedes any ship action.
    - Pass the (when CLI touched) surface and other named gates BEFORE committing — fail closed.
    - Bump the correct per-binary .version on every commit and stage it.
    - Run `release check` locally and confirm it is CLEAN before `git push`.
    - Watch all THREE CI workflows (test, release, deploy) and confirm each conclusion.
    - Use conventional-commit format with NO AI attribution (no Claude/AI/automated/assistant/co-author).
    - Stop on any gate BLOCK or CI failure and report it.
  ask_first:
    - Amending, rewriting, or retrying a commit after a CI failure.
    - `git push --force` / `--force-with-lease`, or any rewrite of already-pushed history.
    - Committing despite a BLOCKED gate (only with an owned register row / tracking story).
    - Reverting or rolling back a pushed change that already triggered deploy.
  never:
    - Force-push or rewrite pushed history without the operator's say-so.
    - Bypass a gate with `--skip-review`, `--no-verify`, or by editing the gate output.
    - Ship from a tree whose techdebt-review traverse failed, or commit on a BLOCKED surface gate.
    - Push a CLIENT_PATH change without a matching `satellites.version` bump.
    - Add AI attribution to a commit message.
```
