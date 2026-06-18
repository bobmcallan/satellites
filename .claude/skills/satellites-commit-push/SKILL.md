---
name: satellites-commit-push
type: skill
kind: capability
when: status==shipping
tags: [kind:capability]
description: Ship satellites at the `shipping` state — after techdebt-review and integration-review have passed, before done-review. Runs the remaining pre-commit gates, bumps .version ONCE, makes the final commit + push (folding the incremental local commits made during in_progress), watches the CI chain (test → release → deploy), and records evidence. The process-owned counterpart of the operator's /commit-push shadow.
---
<!-- satellites-sync:begin {"document_id":"doc_2c4432eb","version":6,"hash":"fd45cc26b013b807be07137a4554d658270b5cd002652329fdf27cf6bc8f6a86"} satellites-sync:end -->

# satellites-commit-push

This capability runs at the workflow's **`shipping`** state (actor: executor):
after both reviewer gates — [[satellites-technical-debt-review]] (senior-dev
code-debt scan) and [[satellites-integration-test-review]] (broken-windows:
build + unit + integration) — have PASSED, and before `done-review`. It is the
single point the change is pushed; done-review judges the pushed commit.

**Commit cadence.** During `in_progress` you commit INCREMENTALLY and LOCALLY as
the work progresses — small conventional commits, **no `.version` bump, no
push**. This step is where the change first leaves the machine: one `.version`
bump, then push (all the incremental commits go up together).

**No AI attribution** in commit messages (no "Claude", "AI", "automated",
"assistant", "co-author"). Conventional commit format: `type(scope): description`.

## Routine

1. **Precondition — you are in the `shipping` state**, i.e. techdebt-review AND
   integration-review have already PASSED for this checkpoint (the broken-windows
   build/unit/integration verdict is integration-review's, rendered against the
   local tree before you ship). Never ship from a tree whose reviews did not
   pass; on a fail you are back in `in_progress`, not here.

   Then run the remaining pre-commit gates — each is its own atomic skill; this
   step only names them and honours their verdicts. A gate's routine, repair
   semantics, and guardrails live in the gate skill — the single home; do not
   restate or improvise them here.

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
     satellites-agent-architecture-review <story-id>`. accept → proceed; reject →
     **do not commit**, move the flagged behaviour into the substrate, re-run.

2. **Configure + stage**

   ```bash
   git config user.name "bobmcallan" && git config core.autocrlf input && git add -A
   ```

3. **Analyse + format** — `git branch --show-current`, `git diff --cached --stat`,
   `git log --oneline -5` for style. If `go.mod` exists: `gofmt -s -w . && git add -u`.

4. **Bump `.version` ONCE for this ship.** `.version` is per-binary:
   `satellites.version` = the **CLI**, `satellites-server.version` = the
   **server**. Bump the patch of the binary(ies) the change touches. This is the
   ONLY bump in the loop — the incremental `in_progress` commits did not bump.

   The release gate's **CLIENT_PATHS** (`.github/workflows/release.yml`) are the
   authoritative definition of "client-affecting" → bump **`satellites.version`**:

   ```
   internal/cli/  cmd/satellites/  internal/verb/  internal/workstate/  internal/workflow/  internal/audit/
   ```

   - **CLI / client** (any CLIENT_PATH above) → bump `satellites.version`.
     `internal/verb` IS a client path (compiled into the CLI).
   - **server-only** (`internal/server`, `internal/mcpserver`,
     `internal/document`, `cmd/satellites-server`, other non-client packages) →
     bump `satellites-server.version`.
   - **both** — code compiled into BOTH binaries bumps both versions:
     `config/documents/` embedded seeds, and `internal/verb`. When in doubt, bump both.
   - **substrate-only** (only `.satellites/` documents/skills/workflows, docs) →
     no binary path changed, so no bump is required.

   Update the matching `*.build` timestamp. `git add .version`. A CLIENT_PATH
   change pushed with no bump FAILS the release workflow.

5. **Final commit, then push.** One commit folds the staged work + the `.version`
   bump on top of the incremental `in_progress` commits; the push sends them all.

   ```bash
   git commit -m "type(scope): message"
   git push
   ```

6. **Watch CI** (`.github/workflows/`: test → release → deploy) — three
   **separate** workflows. Check ALL THREE conclusions, especially **release**: it
   FAILS when a CLIENT_PATH changed without a `satellites.version` bump, invisible
   if you only watch `test` + `deploy`. The **`test`** workflow runs TWO parallel
   jobs — `vet-test` (unit) and `integration` (the full `-tags integration` tier,
   incl. chromedp); either red fails `test` and blocks release+deploy, so CI is
   the non-skippable backstop to the local integration-review gate. When `test` is
   red, `gh run view <id> --log-failed` shows which job.

   ```bash
   HEAD=$(git rev-parse HEAD)
   gh run list --commit "$HEAD" --json workflowName,status,conclusion --jq '.[] | "\(.workflowName): \(.status) \(.conclusion)"'
   gh run watch <test-run-id> --exit-status
   ```

   On failure, surface the failing step (`gh run view <id> --log-failed`) and stop
   — do not amend or retry unless asked. On success, report the runs + the release tag.

7. **Record the CI outcomes into the QA-evidence trail.** Once the three workflows
   have concluded:

   ```bash
   satellites evidence ci --from-head   # story id from HEAD's commit trailer; idempotent
   ```

   It writes a `ci_result` row per concluded stage (test/release/deploy), keyed
   to the story in the commit trailer; a stage with no concluded run is skipped.
   Confirm with `satellites evidence show <story>`. Then advance `shipping →
   done-review` and request `satellites-story-done-review`.

## Environment

Runs inside the satellites repo working tree against its checked-out remote. It is
host-coupled by design: it relies on `.satellites/satellites`, the per-binary
`.version` files, and the release workflow's CLIENT_PATHS — it is not meant to run
in an arbitrary repository. It pushes to the remote and thereby triggers the
deploy workflow, so the bounds below apply to every run.

```yaml
guardrails:
  always:
    - Run only from the shipping state — techdebt-review and integration-review must already have PASSED.
    - Pass the (when CLI touched) surface and other named gates BEFORE committing — fail closed.
    - Bump the correct per-binary .version ONCE for the ship when a binary path changed, and stage it; substrate-only ships need no bump.
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
    - Ship from a tree whose techdebt-review or integration-review did not pass.
    - Push a CLIENT_PATH change without a matching `satellites.version` bump.
    - Bump .version on the incremental in_progress commits — the bump happens once, here.
    - Add AI attribution to a commit message.
```
