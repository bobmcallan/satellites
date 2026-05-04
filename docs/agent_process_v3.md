## Agent process v3 — current substrate

This is the verb chain a Claude session runs to take a story from
`backlog` to `done`. It supersedes `agent_process_v2.md`.

The unit of capability is the **agent doc**. An agent's
`tool_ceiling` enumerates the MCP verbs that agent may call; its
`permission_patterns` enumerates the file/shell patterns it may
touch.

The unit of work is the **task**. A task names an action on a
contract — `implement plan`, `review plan`, `implement develop`,
`review develop`, … — and carries an `agent_id` assigned at
compose time. Implement tasks are explicitly **claimed** by the
implementing agent. Review tasks are **published**; the embedded
reviewer is a long-running subscriber that claims and completes
them automatically — the orchestrator never claims a review task.

Contracts are the spec (markdown docs under `config/seed/contracts/`).
Contract instances (CIs) exist as derived state — bookkeeping that
aggregates the implement+review task pair for one contract on one
story. CIs are not the unit of claim.

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
   no `tasks`; that's the signal to compose a plan.

---

## 4. Compose the workflow

1. **`orchestrator_compose_plan({story_id, agent_overrides?})`** —
   - writes a `kind:plan` ledger row + a `kind:plan-approved` row
     (legacy auto-approval shortcut),
   - writes the `kind:workflow-claim` row,
   - **creates an ordered task list** — for the default contract
     sequence `plan → develop → push → merge_to_main → story_close`
     this produces 10 tasks, paired:
     `implement plan → review plan →
      implement develop → review develop →
      implement push → review push →
      implement merge_to_main → review merge_to_main →
      implement story_close → review story_close`,
   - **mints one ephemeral agent per implement task** with
     permission patterns derived from the contract's needs;
     stamps the agent_id on the task,
   - assigns the persistent reviewer agent to every review task,
   - returns
     `{tasks, plan_ledger_id, workflow_claim_ledger_id,
      agent_assignments}`.

   **Reviewer-loop alternative (preferred for new stories):**
   **`orchestrator_submit_plan({story_id, plan_markdown,
   proposed_contracts, iteration})`** — calls the embedded
   reviewer; loop on `needs_more` until `accepted`. Iteration cap
   from KV `plan_review_max_iterations` (default 5).
   `proposed_contracts` MUST start with `plan` (verified — the
   reviewer rejects otherwise).

2. **`task_walk({story_id})`** — returns the freshly-composed task
   list with `current_task_id` pointing at the first ready
   implement task. Each row is an ordered (action, contract,
   agent_id, status) tuple — this IS the dispatch list.

---

## 5. Per-task cycle

For each implement task returned by `task_walk` (in
`current_task_id` order). Review tasks are not in the
orchestrator's loop — they fire automatically when their paired
implement task closes.

### 5a. Claim
**`task_claim({task_id, agent_id, plan_markdown})`** —
- runs the predecessor gate (the prior task in the ordered list
  must be `closed_success` or `skipped`),
- verifies the agent's capability surface (`agent.tool_ceiling`,
  `agent.permission_patterns`) covers what the task needs,
- writes a `kind:action-claim` row + optional `kind:plan` row,
- flips the task to `claimed`.

The `agent_id` should come from the task's stamped `agent_id`
field returned by `task_walk`. Same-session re-claim is treated
as an amend: prior action-claim + plan rows are dereferenced and
replaced.

### 5b. `implement plan` task specifics

A plan implement task close that doesn't enqueue at least one
downstream task is rejected **by the substrate**. Always:

```
task_enqueue({
  origin: "story_stage",
  parent_task_id: <plan-implement-task-id>,
  agent_id: <minted-or-overridden-agent>,
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

Then `task_close` with all three ids in `evidence_ledger_ids`.

### 5c. `implement develop` task specifics

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

### 5d. `implement push` task specifics

Push contract (`config/seed/contracts/push.md`) is intentionally
thin: `git fetch` then `git push` non-force, evidence is the
verbatim `X..Y main -> main` line + commit SHA + subject + a
"no force / no destructive ops" attestation + a
"`.version` not re-bumped" attestation.

On trunk-based repos the reviewer occasionally misreads the
"develop commit" phrasing in the contract as a branch name —
pre-empt by including a branch-topology proof
(`git branch -a` showing only `main`) in the close evidence.

### 5e. `implement merge_to_main` task specifics

Trunk-based repos: this is a state-only verification. Capture
`git status --short`, `git rev-parse HEAD`,
`git rev-parse origin/main`, and `git rev-parse --abbrev-ref HEAD`
to show local + remote both at the develop commit on `main`.

### 5f. `implement story_close` task specifics

The story template gates the `done` transition on
`field_present:<…>` hooks. Set every `done`-required field via
`story_field_set` BEFORE filing the close (otherwise the
substrate's status flip will fail mid-close):

```
story_field_set({id, field: "fix_commit", value: "<SHA>"})
story_field_set({id, field: "regression_test_path", value: "..."})
…
```

Then `task_claim` → `task_close` the story_close implement task.
The paired review task fires automatically; on success the
substrate flips the story to `done`.
Auto-flip is sometimes unreliable — call
`story_update_status({id, status: "done"})` manually to confirm
if the story stays `in_progress` after the review task closes.

### 5g. Close

**`task_close({task_id, outcome, close_markdown,
evidence_markdown?, evidence_ledger_ids?})`** —
- writes a `kind:close-request` row,
- writes an inline `kind:evidence` row when `evidence_markdown`
  non-empty,
- flips the task to `closed_success` or `closed_failure`,
- **publishes the paired review task** — the substrate emits a
  `task.review_ready` event for the review task. The embedded
  reviewer (Gemini, default) is subscribed to this event kind and
  claims the review task on its own session, runs the rubric
  against the close evidence + every ledger row referenced in
  `evidence_ledger_ids` + the contract body, and closes the
  review task with `verdict ∈ {accepted, rejected}`.

The orchestrator never sees the review task — it's published,
the subscriber consumes it. The orchestrator's next visibility
into the review outcome is `task_walk` returning the next ready
implement task (on accepted) OR a fresh implement task with
`prior_task_id` set (on rejected).

### 5h. On rejected review

The substrate spawns a fresh implement task of the same contract
type with `prior_task_id` set, carrying the review verdict and
review questions as ledger context. The orchestrator's next
`task_walk` shows it as the new `current_task_id`. Read the
latest `kind:verdict` row for the review questions, then either:

- `task_claim` the new implement task with revised
  `plan_markdown` and re-close with stronger evidence, OR
- `task_respond({task_id, response_markdown})` to write a
  `kind:review-response` row that addresses the question,
  then close the new implement task.

The review iteration cap on plan tasks is
`KV.plan_review_max_iterations` (default 5). For non-plan tasks
there's no formal cap — the reviewer will loop until the
evidence package satisfies the rubric.

### 5i. Move on

`task_walk({story_id})` again — `current_task_id` now points at
the next ready implement task. Loop back to 5a.

---

## 6. Where the substrate's setup data lives

`config/seed/` is the single source of truth.

| Subdir | Document type | What lives here |
|---|---|---|
| `agents/` | `agent` | `developer_agent`, `releaser_agent`, `story_close_agent`, `story_reviewer`, `development_reviewer`. The `tool_ceiling` field enforces the agent's capability scope; `permission_patterns` lists the file/shell patterns it may touch. |
| `contracts/` | `contract` | per-contract specs — `category`, `validation_mode`, `evidence_required` body. The contract is the spec; tasks are the execution. |
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
                 │   ↳ creates 10 tasks: implement+review per contract
                 │   ↳ mints ephemeral agent per implement task; reviewer agent on review tasks
                 └─ task_walk                 (orient — single call, returns ordered task list)
                      └─ for each implement task in sequence:
                           ├─ task_claim      (agent_id from task's stamped field)
                           ├─ task_enqueue    (implement plan task ONLY — substrate gate)
                           ├─ ledger_append   (implement plan: plan-md, review-criteria-md, readiness-assessment)
                           ├─ ledger_append   (implement develop: lint output, ac-references, etc.)
                           └─ task_close      (publishes paired review task → reviewer subscriber)
                                ↳ reviewer subscriber claims review task automatically
                                ↳ reviewer closes with verdict ∈ {accepted, rejected}
                                ↳ on rejected: substrate spawns fresh implement task with prior_task_id
                      └─ story_field_set      (every done-required template field BEFORE story_close task)
                      └─ task_claim/close implement story_close task
                      └─ story_update_status  (manual flip to done if auto-flip didn't fire)
```

---

## Field-tested gotchas

These are the rejections seen in practice. Treat them as default
rules until proven otherwise.

1. **`proposed_contracts` MUST start with `plan`.** Reviewer
   rejects "missing plan contract" otherwise even if the plan is
   the task you're currently inside.
2. **`implement plan` close needs `task_enqueue` first.**
   Substrate-level gate — a plan that decomposes nothing is
   rejected before the reviewer sees it. Enqueue at least one
   downstream task against the plan implement task before close.
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
   downstream JSON parsing trips over.
9. **story_close auto-flip is unreliable.** Even after the
   reviewer accepts the story_close task, the story may stay in
   `in_progress`. Call `story_update_status({id, status: "done"})`
   manually to confirm.
10. **Never claim a review task.** The reviewer subscriber owns
    the review-task lane. Manually claiming a review task from
    the orchestrator session is a bug — let the publish/subscribe
    flow run.


## Task lifecycle (sty_c1200f75)

Tasks flow through a small set of statuses defined by
`config/seed/lifecycles/task.md` and resolved at boot via
`task.RegisterLifecycle`.

**States:**

- `planned` — drafted by an agent, persisted, **invisible to
  subscribers**. The agent owns the row; it can edit, reorder, or
  cancel without affecting downstream consumers.
- `published` — committed to the work queue. The reviewer service
  and any future task-claim worker sees the row on the next scan.
- `claimed` — a subscriber has taken ownership.
- `in_flight` — actively being processed (optional intermediate).
- `closed` — terminal with an outcome (success / failure /
  timeout). Eventually moved to `archived` by the retention sweep.
- `enqueued` — pre-c1200f75 legacy state. Treated as
  subscriber-visible during the migration window. New rows should
  not use it.

**Verbs:**

- `task_plan(...)` — write a row at status=planned. Agent's
  drafting state.
- `task_publish(task_id)` — flip a planned row to published. The
  orchestrator's commit step.
- `task_publish(...)` (no `task_id`) — create + publish in one
  call when the agent doesn't need the staging step.
- `task_enqueue(...)` — legacy alias; writes at status=enqueued.
  Kept for back-compat. New code should use `task_plan` +
  `task_publish` explicitly.

**Subscriber rule:** workers (reviewer service, future task-claim
runtimes) filter by `task.SubscriberVisibleStatuses()` —
`{published, enqueued, claimed, in_flight}` by default. `planned`
rows are never visible.

**Why a separate planned state:** an agent's working list
(drafting, reordering, batching) is not the same as the committed
queue. Conflating them — as `task_enqueue` did before c1200f75 —
leaks half-formed plans into the subscriber's view and removes the
orchestration opportunity. Splitting the states gives the
orchestrator a place to stand: plan locally, then publish.
