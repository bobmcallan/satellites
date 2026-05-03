## Agent process v3 — current substrate (post role-tier retirement)

This is the post-implementation snapshot of the verb chain a Claude
session runs to take a story from `backlog` to `done`. It supersedes
`agent_process_v2.md` after the role tier was retired
(sty_c67ad430, sty_0f7cea24, commit `2aca005`) and the lived flow
shifted to make the **agent doc the sole capability tier** and the
**embedded reviewer non-trivial to satisfy**.

If v2 says "agent_role_claim before contract_claim" — v3 says
the role is implicit in the agent doc; you call agent_role_claim
only when you want the role-grant audit row.

---

## What changed vs `agent_process_v2.md`

| Area | v2 | v3 (now) |
|---|---|---|
| Capability tier | Role doc + agent doc | **Agent doc only** (sty_c67ad430). `config/seed/roles/` directory deleted. Agent's `tool_ceiling` is the authority. |
| `agent_role_claim` | Required before every `contract_claim` | Still wired (`internal/mcpserver/grant_handlers.go:21`) but `role_id` is optional. **Most flows skip it** — `contract_claim({agent_id})` resolves the agent and checks its tool ceiling directly. |
| `permissions_claim` arg on `contract_claim` | Documented array of patterns | **Retired** (`claim_handlers.go:103-108`) — calls return `permissions_claim_retired`. Permission patterns come from the agent doc. |
| `required_role` on contracts | Field in YAML frontmatter | **Removed from seed** — see `config/seed/contracts/*.md`. Gate code keeps a legacy fallback but seed writes none. The contract's `category` and the orchestrator's `agent_assignments` map drive routing now. |
| Plan-CI close | Just write `kind:close-request` row | **Plan close requires ≥1 task enqueued against the plan CI** (`close_handlers.go:95-107`, error `plan_close_requires_tasks`). A plan that decomposes nothing is rejected by the substrate before the reviewer even sees it. |
| Plan close evidence | "Inline kind:evidence row" | **Three artifacts the reviewer hard-rejects without** (`config/seed/contracts/plan.md`): `plan.md` (scope, files-to-change, approach, test strategy, AC mapping), `review-criteria.md` (per-AC verify / evidence / pass-fail boundary), and a readiness assessment (relevance, dependencies, prior delivery). File these as `type=artifact` ledger rows and pass their ids in `evidence_ledger_ids`. |
| Develop close evidence | Test results | Test results **plus** `go vet ./...` + `gofmt -l .` output, the full commit message, `.version` patch-bump confirmation, and **file:line / SHA references for every AC** — declarative "AC met by construction" is auto-rejected. |
| Push contract | "Ship the develop commit" | Same intent (`config/seed/contracts/push.md:14-22`); branch-agnostic ("current branch's upstream"). The Gemini reviewer occasionally misreads "develop commit" as a branch name on trunk-based repos — call it out in close evidence. |
| Pre-existing flake handling | Undocumented | Reviewer expects a **`git stash -u --keep-index` round-trip** (or worktree round-trip on HEAD~1 if you've already committed). Just saying "this is pre-existing" gets rejected. |
| Orientation verb | `story_get` + `contract_next` + `task_list` | **`task_walk({story_id})`** — single roundtrip returning `{story, contract_instances, current_ci_id, configuration_name}` (`task_walk_handler.go:18-60`). Returns CI list only — not separate "ready tasks". |
| Reviewer in-loop | Body-arg `session_id` | Header-only under Streamable HTTP; embedded Gemini reviewer is the gate (`SATELLITES_REVIEWER_SERVICE=embedded`). |

---

## 1. Session bootstrap

1. **`session_register({})`** — server mints UUIDv4 and returns it
   via the `Mcp-Session-Id` header on `initialize`. Spec-compliant
   clients echo it on every call. Body-arg `session_id` is
   stdio/test-only.
2. **`session_register({project_id})`** — resume semantics. Returns
   the most recent non-stale session for `(user, project)` if one
   exists (`resumed=true`).
3. (Optional) **`session_whoami({})`** — returns
   `effective_verbs` from the agent's `tool_ceiling` (NOT a role
   doc — that tier is gone).

---

## 2. Project context

1. `git remote get-url origin`.
2. **`project_set({repo_url})`** — binds the session to the project
   that owns the canonicalised remote.
3. **On miss** (`status: no_project_for_remote`) ask the user
   before `project_create`.

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
   - assigns a system agent to each CI via the
     `agentRoleForContract` map
     (`orchestrator_compose.go:231-247`):
     plan/develop → `developer_agent`,
     push/merge_to_main → `releaser_agent`,
     story_close → `story_close_agent`,
   - returns
     `{contract_instances, plan_ledger_id, workflow_claim_ledger_id,
      task_ids, agent_assignments}`.

   **Reviewer-loop alternative (preferred for new stories):**
   **`orchestrator_submit_plan({story_id, plan_markdown,
   proposed_contracts, iteration})`** — calls the embedded
   reviewer; loop on `needs_more` until `accepted`. Iteration cap
   from KV `plan_review_max_iterations` (default 5).
   `proposed_contracts` MUST start with `plan` (verified — the
   reviewer rejects otherwise).

2. **`task_walk({story_id})`** — returns the freshly-composed CI
   list with `current_ci_id` pointing at the first non-terminal
   CI.

---

## 5. Per-CI cycle

There is no per-CI role grant in v3. The agent doc is the
authority. Optional `agent_role_release` / `agent_role_claim`
calls remain for callers that want the audit-row trail, but the
contract claim does not require an active grant.

For each CI returned by `task_walk`:

### 5a. Claim
**`contract_claim({contract_instance_id, agent_id, plan_markdown,
skills_used?})`** —
- runs the predecessor gate (predecessors must be
  `passed`/`skipped`),
- resolves agent permissions from the agent doc (`agent_id`
  required; `permissions_claim` is RETIRED — pass it and you get
  `permissions_claim_retired`),
- writes a `kind:action-claim` row + optional `kind:plan` row,
- flips the CI to `claimed`.

`agent_id` resolution order
(`claim_handlers.go:81-109`): explicit arg → session's
`OrchestratorGrantID` → grant's `AgentID` → error.

### 5b. Plan CI specifics

A plan close that doesn't enqueue at least one task is rejected
**by the substrate**, not the reviewer. Always:

```
task_enqueue({
  origin: "story_stage",
  contract_instance_id: <plan-ci-id>,
  required_role: "developer",   // or releaser, etc.
  payload: <json>
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
Reviewer flips it to `passed`; substrate flips story to `done`.

### 5g. Close

**`contract_close({contract_instance_id, close_markdown,
evidence_markdown?, evidence_ledger_ids?})`** —
- writes a `kind:close-request` row,
- writes an inline `kind:evidence` row when `evidence_markdown`
  non-empty,
- enqueues a `kind:review` task (`required_role=reviewer`) and
  flips the CI to `pending_review` (production
  `validation_mode=task`),
- the embedded Gemini reviewer claims that task on its own
  session and runs the rubric against the close evidence + every
  ledger row referenced in `evidence_ledger_ids` + the contract
  body.

### 5h. On rejected

The substrate appends a fresh CI of the same contract type with
`PriorCIID` set. Read the latest `kind:verdict` row for the
review questions, address them, then either:

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

`config/seed/` is the single source of truth. After role-tier
retirement (sty_c67ad430) the layout is:

| Subdir | Document type | What lives here |
|---|---|---|
| `agents/` | `agent` | `developer_agent`, `releaser_agent`, `story_close_agent`, `story_reviewer`, `development_reviewer` (and gemini reviewer agents). `permitted_roles` is the legacy field, `tool_ceiling` is what enforces. |
| `contracts/` | `contract` | per-slot contracts — `category`, `validation_mode`, `evidence_required` body. `required_role` removed from frontmatter. |
| `workflows/` | `workflow` | prose workflow descriptions (slot algebra retired in story_af79cf95). |
| `artifacts/` | `artifact` | `default_agent_process` — the handshake markdown the MCP server returns as its `instructions` block. |
| `principles/` | `principle` | enforced rules (`pr_evidence`, `pr_root_cause`, `pr_no_unrequested_compat`, `pr_skills_reviewers_ad_hoc`, …). |
| `story_templates/` | `story_template` | per-category required + done-hook fields. |
| `replicate_vocabulary/` | `replicate_vocabulary` | natural-language → `portal_replicate` action mapping. |
| ~~`roles/`~~ | — | **DELETED** in commit `2aca005` (sty_c67ad430). |

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
                 └─ task_walk                 (orient — single call, not story_get + contract_next + task_list)
                      └─ for each CI in sequence:
                           ├─ contract_claim  (agent_id required; no permissions_claim; no role grant needed)
                           ├─ task_enqueue    (plan CI ONLY — substrate gate)
                           ├─ ledger_append   (plan CI: plan-md, review-criteria-md, readiness-assessment)
                           ├─ ledger_append   (develop CI: lint output, ac-references, etc.)
                           └─ contract_close  (evidence_ledger_ids carries every artefact id)
                                ↳ kind:review task → embedded Gemini reviewer →
                                   contract_review_close → CI flips passed / failed
                                ↳ on rejected: substrate appends fresh CI; address verdict
                                   review_questions on next claim
                      └─ story_field_set      (every done-required template field BEFORE story_close CI)
                      └─ (story_close CI flips story to done)
```

---

## Field-tested gotchas

These are the rejections I've seen in practice that v2 doesn't
warn about. Treat them as default rules until proven otherwise.

1. **`proposed_contracts` MUST start with `plan`.** Reviewer
   rejects "missing plan contract" otherwise even if the plan is
   the CI you're currently inside.
2. **Plan close needs `task_enqueue` first.** Substrate-level
   gate, returns `plan_close_requires_tasks` from
   `contract_close`. Enqueue at least one downstream-role task
   against the plan CI before close.
3. **`evidence_ledger_ids` must reference rows that contain the
   actual content** the reviewer asks for — not summaries. Write
   the plan body / criteria / readiness as their own `type=artifact`
   rows.
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
