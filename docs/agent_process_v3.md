## Agent process v3 — current substrate

This is the verb chain a Claude session runs to take a story from
`backlog` to `done`. It supersedes `agent_process_v2.md`.

The unit of capability is the **agent doc**. An agent's
`tool_ceiling` enumerates the MCP verbs that agent may call; its
`permission_patterns` enumerates the file/shell patterns it may
touch. The orchestrator's `agent_assignments` map (returned by
`orchestrator_compose_plan`) binds one agent to each contract
instance. There is no separate primitive between the agent and
the contract instance — the agent IS what authorises the work.

---

## 1. Session bootstrap

1. **`session_register({})`** — server mints a UUIDv4 and returns
   it via the `Mcp-Session-Id` header on `initialize`.
   Spec-compliant clients echo it on every call. Body-arg
   `session_id` is stdio/test-only.
2. **`session_register({project_id})`** — resume semantics. Returns
   the most recent non-stale session for `(user, project)` if one
   exists (`resumed=true`).
3. (Optional) **`session_whoami({})`** — smoke check; returns
   `effective_verbs` derived from the active agent's
   `tool_ceiling`.

---

## 2. Project context

1. `git remote get-url origin` (shell).
2. **`project_set({repo_url})`** — binds the session to the project
   that owns the canonicalised remote.
3. **On miss** (`status: no_project_for_remote`) ask the user
   before calling `project_create`.

Subsequent project-scoped verbs default to the bound project.

---

## 3. Inspect the story

1. **`story_get({id})`** — full story body, ACs, status, template
   hooks (`feature` / `bug` / `improvement` / `infrastructure` /
   `documentation`).
2. (Optional) **`task_walk({story_id})`** — single-roundtrip
   orientation. If the story has never been planned this returns
   no `contract_instances`; that's the signal to compose a plan.

---

## 4. Compose the workflow

1. **`orchestrator_compose_plan({story_id, agent_overrides?})`** —
   - writes a `kind:plan` ledger row + a `kind:plan-approved` row
     (legacy auto-approval shortcut),
   - writes the `kind:workflow-claim` row,
   - creates one CI per slot (default sequence
     `plan → develop → push → merge_to_main → story_close`),
   - **assigns one agent to each CI** deterministically:
     `plan` and `develop` → `developer_agent`,
     `push` and `merge_to_main` → `releaser_agent`,
     `story_close` → `story_close_agent`,
   - returns
     `{contract_instances, plan_ledger_id, workflow_claim_ledger_id,
      task_ids, agent_assignments}`.

   The `agent_assignments` map is the dispatch source of truth —
   each CI's assigned agent is what `contract_claim` will
   authorise the work under.

   **Reviewer-loop alternative (preferred for new stories):**
   **`orchestrator_submit_plan({story_id, plan_markdown,
   proposed_contracts, iteration})`** — calls the embedded
   reviewer; loop on `needs_more` until `accepted`. Iteration cap
   from KV `plan_review_max_iterations` (default 5).
   `proposed_contracts` MUST start with `plan` (verified — the
   reviewer rejects otherwise).

2. **`task_walk({story_id})`** — returns the freshly-composed CI
   list with `current_ci_id` pointing at the first non-terminal
   CI. The CI list IS the task list — each row represents an
   ordered (action, contract) pair the orchestrator will dispatch.

---

## 5. Per-CI cycle

For each CI returned by `task_walk` (in `current_ci_id` order):

### 5a. Claim
**`contract_claim({contract_instance_id, agent_id, plan_markdown,
skills_used?})`** —
- runs the predecessor gate (predecessors must be
  `passed`/`skipped`),
- resolves the agent doc by id and verifies its capability surface
  (`agent.tool_ceiling`, `agent.permission_patterns`),
- writes a `kind:action-claim` row + optional `kind:plan` row,
- flips the CI to `claimed`.

The `agent_id` should come from the `agent_assignments` map
returned by `orchestrator_compose_plan`. Same-session re-claim is
treated as an amend: prior action-claim + plan rows are
dereferenced and replaced.

### 5b. Plan CI specifics

A plan close that doesn't enqueue at least one task is rejected
**by the substrate**, not the reviewer. Always:

```
task_enqueue({
  origin: "story_stage",
  contract_instance_id: <plan-ci-id>,
  payload: <json describing the work item>
})
```

THEN file the three plan artefacts as ledger rows:

```
ledger_append({
  type: "artifact",
  tags: ["kind:plan-md", "phase:plan", "iteration:<n>"],
  content: <plan body>
})
ledger_append({
  type: "artifact",
  tags: ["kind:review-criteria-md", "phase:plan", "iteration:<n>"],
  content: <per-AC criteria>
})
ledger_append({
  type: "evidence",
  tags: ["kind:readiness-assessment", "phase:plan", "iteration:<n>"],
  content: <relevance/deps/prior delivery>
})
```

Then `contract_close` with all three ids in `evidence_ledger_ids`.

### 5c. Develop CI specifics

The reviewer rubric for `develop` (per the lived flow, not yet
formally documented in seed) demands:

- Test results: PASS/FAIL list per new test + full package run.
- `go vet ./...` output (must be clean).
- `gofmt -l .` output (must be clean).
- Full commit message body — including conventional-commit
  subject, scope, story id, and absence of AI attribution.
- `.version` patch-bump confirmation: bumped exactly once,
  parent → child SHA captured.
- Per-AC concrete reference: file:line, test name, command output,
  or commit SHA. **Declarative "AC met by construction" gets
  rejected.** Verify scope-only ACs (e.g. "no application code")
  with grep-exclusion output:
  ```
  $ git show --name-only HEAD | grep -v -E "_test\.go$|^\.version$"
  (no output)
  ```
- For pre-existing flakes you want to surface as out-of-scope: a
  worktree round-trip on `HEAD~1` (or `git stash -u --keep-index`
  round-trip if uncommitted) showing the same flake rate
  pre-change. Anything less gets rejected.

### 5d. Push CI specifics

Push contract (`config/seed/contracts/push.md`) is intentionally
thin: `git fetch` then `git push` non-force, evidence is the
verbatim `X..Y main -> main` line + commit SHA + subject + a
"no force / no destructive ops" attestation + a
"`.version` not re-bumped" attestation.

On trunk-based repos the reviewer occasionally misreads the
"develop commit" phrasing in the contract as a branch name —
pre-empt by including a branch-topology proof
(`git branch -a` showing only `main`) in the close evidence.

### 5e. merge_to_main CI

Trunk-based repos: this is a state-only verification. Capture
`git status --short`, `git rev-parse HEAD`,
`git rev-parse origin/main`, and `git rev-parse --abbrev-ref HEAD`
to show local + remote both at the develop CI's commit on `main`.

### 5f. story_close CI

The story template gates the `done` transition on
`field_present:<…>` hooks. Set every `done`-required field via
`story_field_set` BEFORE filing the close (otherwise the
substrate's status flip will fail mid-close):

```
story_field_set({id, field: "fix_commit", value: "<SHA>"})
story_field_set({id, field: "regression_test_path", value: "..."})
…
```

Then `contract_claim` → `contract_close` the story_close CI.
Reviewer flips the CI to `passed`. The story's auto-flip to
`done` does NOT always fire — call `story_update_status({id,
status: "done"})` manually after the reviewer accepts to confirm
the transition.

### 5g. Close

**`contract_close({contract_instance_id, close_markdown,
evidence_markdown?, evidence_ledger_ids?})`** —
- writes a `kind:close-request` row,
- writes an inline `kind:evidence` row when `evidence_markdown`
  non-empty,
- enqueues a `kind:review` task and flips the CI to
  `pending_review` (production `validation_mode=task`),
- the embedded Gemini reviewer claims that task on its own
  session and runs the rubric against the close evidence + every
  ledger row referenced in `evidence_ledger_ids` + the contract
  body.

### 5h. On rejected

The substrate appends a fresh CI of the same contract type with
`PriorCIID` set. Read the latest `kind:verdict` row for the
review questions, then either:

- `contract_claim` the new CI with revised `plan_markdown` and
  re-close with stronger evidence, OR
- `contract_respond({contract_instance_id, response_markdown})`
  to write a `kind:review-response` row that addresses the
  question, then close the new CI.

The plan-iteration cap is `KV.plan_review_max_iterations`
(default 5). For non-plan CIs there's no formal cap — the
reviewer will loop until the evidence package satisfies the
rubric.

### 5i. Move on

`task_walk({story_id})` again — `current_ci_id` now points at
the next non-terminal CI. Loop back to 5a.

---

## 6. Where the substrate's setup data lives

`config/seed/` is the single source of truth.

| Subdir | Document type | What lives here |
|---|---|---|
| `agents/` | `agent` | `developer_agent`, `releaser_agent`, `story_close_agent`, `story_reviewer`, `development_reviewer`. The `tool_ceiling` field enforces the agent's capability scope; `permission_patterns` lists the file/shell patterns it may touch. |
| `contracts/` | `contract` | per-slot contracts — `category`, `validation_mode`, `evidence_required` body. |
| `workflows/` | `workflow` | prose workflow descriptions (slot algebra retired in story_af79cf95). |
| `artifacts/` | `artifact` | `default_agent_process` — the handshake markdown the MCP server returns as its `instructions` block. |
| `principles/` | `principle` | enforced rules (`pr_evidence`, `pr_root_cause`, `pr_no_unrequested_compat`, `pr_skills_reviewers_ad_hoc`, …). |
| `story_templates/` | `story_template` | per-category required + done-hook fields. |
| `replicate_vocabulary/` | `replicate_vocabulary` | natural-language → `portal_replicate` action mapping. |

Skills + reviewers (`type=skill`, `type=reviewer`) are not seeded
— ad-hoc by principle (`pr_skills_reviewers_ad_hoc`).

`system_seed_run` is idempotent. Edit a markdown file under
`config/seed/`, redeploy or call `system_seed_run`, and the
substrate converges.

---

## Quick reference: the verb chain for one story

```
session_register                              (server mints id; header carries it)
  └─ project_set                              (or project_create on miss)
       └─ story_get
            └─ orchestrator_compose_plan      (or orchestrator_submit_plan loop)
                 └─ task_walk                 (orient — single call)
                      └─ for each CI in sequence:
                           ├─ contract_claim  (agent_id required; resolves directly)
                           ├─ task_enqueue    (plan CI ONLY — substrate gate)
                           ├─ ledger_append   (plan CI: plan-md, review-criteria-md, readiness-assessment)
                           ├─ ledger_append   (develop CI: lint output, ac-references, etc.)
                           └─ contract_close  (evidence_ledger_ids carries every artefact id)
                                ↳ kind:review task → embedded Gemini reviewer →
                                   contract_review_close → CI flips passed / failed
                                ↳ on rejected: substrate appends fresh CI; address verdict
                                   review_questions on next claim
                      └─ story_field_set      (every done-required template field BEFORE story_close CI)
                      └─ contract_claim/close story_close CI
                      └─ story_update_status  (manual flip to done — auto-flip is unreliable)
```

---

## Field-tested gotchas

These are the rejections seen in practice walking sty_de9f10f9.
Treat them as default rules until proven otherwise.

1. **`proposed_contracts` MUST start with `plan`.** Reviewer
   rejects "missing plan contract" otherwise even if the plan is
   the CI you're currently inside.
2. **Plan close needs `task_enqueue` first.** Substrate-level
   gate, returns `plan_close_requires_tasks` from
   `contract_close`. Enqueue at least one task against the plan
   CI before close.
3. **`evidence_ledger_ids` must reference rows that contain the
   actual content** the reviewer asks for — not summaries. Write
   the plan body / criteria / readiness as their own
   `type=artifact` rows.
4. **Develop close needs lint output even when clean.** "go vet
   clean" stated declaratively gets rejected; paste the empty
   output of `go vet ./...` and `gofmt -l .` verbatim.
5. **AC verification needs concrete refs.** file:line, test name,
   commit SHA, command output. "Met by construction" or "verified
   manually" → reject.
6. **Pre-existing test flakes need a worktree round-trip on
   HEAD~1** (or the rubric's `git stash -u --keep-index`
   round-trip). Just stating "this is pre-existing" → reject.
7. **Trunk-based push** — call out branch topology in the close
   evidence (`git branch -a`). The reviewer reads the contract's
   "develop commit" phrasing literally and may flag a `main`
   push as off-contract until corrected.
8. **`story_field_set` BEFORE `story_update_status`** — the
   category template's hooks gate the transition. A missed field
   returns the failure prose as the tool's text content, which
   downstream JSON parsing trips over (root cause documented in
   sty_de9f10f9).
9. **story_close auto-flip is unreliable.** Even after the
   reviewer accepts the story_close CI, the story may stay in
   `in_progress`. Call `story_update_status({id, status: "done"})`
   manually to confirm.
