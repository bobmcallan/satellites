# Reachability report — entry-point → invoker → data map (2026-06-23)

**Story:** sty_d739fc2f (epic:code-reachability) · validates the function-map
approach before the shipped `satellites code map` (sty_8834343c). **Analysis only —
no code is deleted here** (deletion is sty_d78b0c11, kill-N1).

## Method

The deadness signal is **semantic, not Go-unreachable**: every registered command
"reaches" from `main`, so `deadcode`/`unused` miss the `evidence` shape. The real
test, applied per entry point:

> **ORPHAN** = an entry point with **no system invoker** *whose produced data is
> consumed only by other un-invoked entry points*.

"System invoker" = something other than a human/agent at a terminal: a
`.claude/settings.json` hook, a reviewer skill's functional check, a
`.github/workflows` step, a `scripts/` shell call, or an internal Go caller
(`exec.Command`, `dispatchVerb`). A command an **agent runs because a reviewer
skill documents it** (e.g. `story get`, `workflow embed`, `ledger list`,
`code search`) is **LIVE** — the documented process is its invoker. A command no
process names AND whose data dead-ends is **dead**.

Four surfaces were enumerated from their **registries** (not text grep): the cobra
command tree, the server route table (`internal/server`), the MCP `exposedVerbs`
list + verb registry (`internal/verb`), and the hook wiring
(`.claude/settings.json`). Invokers were scanned across `config/skills`,
`.claude/skills`, `.github/workflows`, `scripts/`, `.claude/settings.json`, and
internal Go dispatch. Data edges are the ledger kinds / store tables each path
produces vs consumes.

---

## 1. Entry points by surface

### 1a. Hooks — `.claude/settings.json` (the harness-fired surface)

Every hook entry is a **system invoker** (the Claude Code harness fires it). All
are LIVE by construction.

| Hook event | Command | Handler | file:line |
|---|---|---|---|
| PreToolUse(Edit\|Write\|MultiEdit\|NotebookEdit) | `hook gate` | `runHookGate` | cmd_hook.go:108 |
| PreToolUse(Bash) | `hook commitgate` | `runHookCommitGate` | cmd_hook_commitgate.go:78 |
| PreToolUse(mcp__satellites__document_get) | `hook access` | `runHookAccess` | cmd_hook_access.go:102 |
| PreToolUse(Read\|Grep\|Bash) | `hook codenudge` | `runHookCodeNudge` | cmd_hook_codenudge.go:80 |
| PreToolUse(document_get) + SessionStart | `hook context` | `runHookContext` | cmd_hook_context.go:77 |
| Stop | `hook stopcheck` | `runHookStopCheck` | cmd_hook_stopcheck.go:67 |
| UserPromptSubmit | `hook prompt` | `runHookPrompt` | cmd_hook_access.go:116 |
| SessionStart | `code index` | `runCodeIndex` | cmd_code.go:100 |

### 1b. CLI — cobra command tree (66 leaf commands / 28 families)

Per-command: handler `file:line`, its **system invoker** (or *manual/agent-only*),
and the ledger/store data it touches. Families with no reachability question are
collapsed; the candidate-orphan families are expanded.

| Command | Handler (file:line) | System invoker | Data produced→consumed | Verdict |
|---|---|---|---|---|
| `code index` | cmd_code.go:100 | **settings.json SessionStart** | writes `.satellites/index.db` ← read by `code search/symbol` | LIVE |
| `code search` / `code symbol` | cmd_code.go:121 / :141 | agent-only (per bootstrap + codenudge) | reads index.db | LIVE (documented) |
| `hook *` (7) | §1a | **settings.json** | per-hook | LIVE |
| `story status_transition` | cmd_story_review.go:310 | agent (workflow process) | appends `review_*`, `status_transition`, `step_summary` ← read by server story-detail | LIVE |
| `story get/set-status/children/validate` | cmd_story_*.go | agent + reviewer skills | reads story rows / appends status_transition | LIVE |
| `workflow list` / `embed` | cmd_workflow_list.go:147 / cmd_workflow_embed.go:54 | **reviewer skill** (intent-plan-review §) | reads/writes `workflow:` selector | LIVE |
| `workflow show/design/upsert/validate` | cmd_workflow_*.go | agent + skills | substrate CRUD | LIVE |
| `workflow check` | cmd_workflow_check.go:677 | **manual-only** (no hook/CI/skill) | produces **gate-shape `ci_result`** (recordGateVerdict, gate_verdict.go:730 call) ← read by AnnotateLedger (live UI) | MANUAL gate — live consumer |
| `surface check` | cmd_surface.go:115 | **manual-only** (no hook/CI/skill) | produces **gate-shape `ci_result`** (cmd_surface.go:90) ← read by AnnotateLedger (live UI) | MANUAL gate — live consumer |
| `evidence show` | cmd_evidence.go:198 | **NONE** | reads evidence rows | **ORPHAN** |
| `evidence ci` (+`--from-head`, cmd_evidence_fromhead.go) | cmd_evidence.go:258 | **NONE** | produces **stage-shape `ci_result`** ← read only by `processtrace.Audit` (dead) | **ORPHAN** |
| `evidence audit` | cmd_evidence_audit.go:34 | **NONE** | consumes `ci_result`/status via `processtrace.Audit` | **ORPHAN** |
| `evidence review` (+ delta helpers, cmd_evidence_delta.go) | cmd_evidence_review.go:235 | **NONE** | consumes via `Reconcile`+`Audit`; writes metrics.json | **ORPHAN** |
| `ledger list` | cmd_ledger.go:52 | agent + reviewer skills | reads ledger | LIVE |
| `exec <verb>` | cmd_exec.go | agent + internal | dispatches verb registry | LIVE |
| `auth` `install` `init` `deploy` `rebase` `update` `validate` `status` `version` `clear` | cmd_*.go | operator/agent (setup loop) | setup/CRUD | LIVE |
| `document/principle/skill/seed/changelog/semantic-search/project/task/work/workspace *` | cmd_*.go | agent + reviewer skills + work hooks | substrate CRUD / engagement | LIVE |

**Dead source (defined, never registered):** `cmd_evidence_delta.go`
(`compareReviewMetrics`, `renderDeltaMarkdown`) is *not* a subcommand — its helpers
are called only by the orphaned `evidence review` (`--compare`). It dies with the
evidence cluster.

### 1c. Server — HTTP routes (`internal/server`)

The server is reached over HTTP; routes are LIVE entry points (browser / MCP
client / `/api/v1/exec`). The reachability-relevant routes:

| Route | Handler (file:line) | Data |
|---|---|---|
| `GET /stories/{id}/trace.fragment` | story_detail.go:267 → buildStoryDetail | **consumes the ledger via `processtrace.AnnotateLedger` (story_detail.go:180) + `Reconcile` (:185)** |
| `GET /ledger` | ledger.go:109 | `ledger_list` → `AnnotateLedger` |
| `GET /projects/{id}` (+ story panel) | project_detail.go:118 | story rows |
| `GET /settings/api-keys` (+ `POST`) | apikeys.go:44 / :75 | **`IssueAPIKey` / `ListAPIKeysForUser`** (apikey store) |
| `GET /settings/system-kv` | system_kv.go:49 | variable store (via verb) |
| `POST /mcp`, `POST /mcp/` | mcpserver/server.go:323 | MCP JSON-RPC → verb dispatch |
| `POST /api/v1/exec/{verb}` | exec.go:22 | CLI verb dispatch → same registry |
| OAuth/login/invite/blob/workspace/changelog/help/events… | (see inventory) | auth / corpus / SSE |

`processtrace.AnnotateLedger` + `Reconcile` are **LIVE** here (the story-detail
trace). They read `ci_result` rows of **both** shapes (gate + stage,
processtrace.go:350) but are repo-agnostic — they carry **none** of the N1 literals.

### 1d. MCP — `exposedVerbs` (26 advertised) + verb registry

The MCP transport advertises exactly the verbs in
`internal/mcpserver/server.go:202` `exposedVerbs` (26): `document_{get,list,count,
upsert,delete}`, `project_{match,create,list,get,update}`, `apikey_{create,list,
revoke}`, `semantic_search`, `workspace_{objective_generate,upsert,archive,
member_*}`, `project_member_*`, `system_status`. These are LIVE (MCP-client
reachable). Verbs **registered but not exposed** (`ledger_{append,list}`,
`variable_{get,set,list,delete}`, `invitation_*`, `*_seed_apply`, `version`,
`story_summary_regenerate`, `workspace_{create,list,get,…}`, `project_access`) are
**exec/internal-only** entry points — reachable via `satellites exec` /
`/api/v1/exec/{verb}` / internal `dispatchVerb`, not as MCP tools. Each has an
internal Go caller (below), so none is orphaned.

---

## 2. ORPHAN list (with confidence + evidence)

### 2a. CONFIRMED DEAD — the `evidence`-ci / QA-audit cluster · confidence HIGH

| Element | file:line | Why dead |
|---|---|---|
| `evidence show` | cmd_evidence.go:198 | no system invoker |
| `evidence ci` (writer) | cmd_evidence.go:258 | no system invoker; sole producer of **stage-shape** `ci_result` |
| `evidence ci --from-head` | cmd_evidence_fromhead.go (`ciStages`) | meant for CI, **CI never calls it** |
| `evidence audit` | cmd_evidence_audit.go:34 | no invoker; reads via `processtrace.Audit` |
| `evidence review` (+ `cmd_evidence_delta.go`) | cmd_evidence_review.go:235 | no invoker; reads via `Reconcile`+`Audit` |
| `processtrace.Audit` / `AuditAll` / `auditUngatedDeploy` / `auditShipThenNoGate` | audit.go:102,122,244,161 | reachable **only** from `evidence audit`/`review` (audit.go callers: cmd_evidence_audit.go:53, cmd_evidence_review.go:267) |
| `validCIStages` / `ciStages` / `Stage=="deploy"` (N1a/b/c) | cmd_evidence.go:34, fromhead.go:23, audit.go:253 | the literals live **only** inside the dead cluster |

**Evidence the cluster is dead:**
1. **No invoker anywhere** — `.claude/settings.json` wires `hook *` + `code index`
   only (no `evidence`); no `config/skills`, `.github/workflows`, `scripts/`, or
   internal Go caller invokes `evidence`. Only non-Go reference to "evidence ci" in
   the repo is the audit report itself.
2. **Proof by ledger** — `sty_028c3f92` ran the full test→release→deploy chain (its
   commit-push review says *"test/release/deploy all completed=success"*) yet its
   ledger has **zero `ci_result` rows**. The writer was never on the CI path.
3. **The GitHub invoker is gone** — `scripts/record-ci-evidence.sh` does not exist;
   it was folded into the verb (`cmd_evidence_fromhead.go:3`), and nothing calls the
   verb.
4. **The `ci_result` consumer that *is* live** (`AnnotateLedger` in the story-detail
   UI) receives no stage-shape rows in practice, and the **logic** consumer of
   stage rows (`processtrace.Audit`) is itself only reachable from the dead
   commands — a wholly un-invoked producer→consumer cluster.

**KEEP (live, must be preserved by the kill story):** `processtrace.Reconcile`,
`AnnotateLedger`, `LedgerEntry` and everything `internal/server/story_detail.go`
imports; `recordGateVerdict` / gate-shape `ci_result` (still used by `surface
check` / `workflow check`). These carry none of the N1 literals.

### 2b. RESOLVED candidates (rough-grep flags → verdict)

| Candidate | Verdict | Evidence | Confidence |
|---|---|---|---|
| **`evidence`** | **DEAD** | §2a | HIGH |
| **`surface`** (`surface check`) | **LIVE feature, manual-only invoker** — *not dead* | no hook/CI/skill fires it, but it produces gate-shape `ci_result` consumed by the live `AnnotateLedger` story-detail UI (cmd_surface.go:90 → story_detail.go:180). It is the intended command-surface drift gate, run in the client-change loop. **Caveat: un-wired** — nothing auto-enforces it (a drift risk worth a follow-up, not a deletion). | HIGH |
| **`variable`** | **LIVE** — *not dead* | no top-level CLI command; the `variable_*` verbs are exec/internal-only, but the **store is heavily live**: server-boot config reads (`main.go:270,288`), dev-login reconcile (`main.go:374`), secret/template resolution (`verb/document.go:1683`), and max-iteration lookup (`cmd_story_review.go:833`, `variable_get`). | HIGH |
| **`apikey`** | **LIVE** — *not dead* | `apikey_create` is MCP-exposed AND minted by `cmd_auth.go:324` (executor-key mint over `/api/v1/exec/apikey_create`); the `/settings/api-keys` portal route writes (`IssueAPIKey`, apikeys.go:75) and reads (`ListAPIKeysForUser`, apikeys.go:106); `ValidateKeyWithRole` gates every authed request. | HIGH |

The rough grep also mis-flagged `commitgate`/`hook`/`gate` — all **LIVE** via
`.claude/settings.json` (§1a). The method must read hook wiring, not just skills.

---

## 3. Spec for the shipped `satellites code map` (sibling story sty_8834343c)

The inputs this one-shot trace used — the exact registries `code map` must walk:

1. **Entry points**
   - **CLI:** walk the cobra command tree (root `rootCmd` → `Commands()` recursively)
     for every leaf + its `RunE` symbol. *Do not* use a literal command list
     (repo-agnostic, per epic:enforcement-surface).
   - **Server:** the route table in `internal/server` (the mux registration site).
   - **MCP:** `internal/mcpserver` `exposedVerbs` for advertised tools **plus** the
     full `internal/verb` registry (`Register(&Verb{…})` in each `init()`) for
     exec/internal-only verbs; flag the exposed/unexposed split.
   - **Hooks:** parse `.claude/settings.json` hook matchers → the `satellites …`
     command each fires (this is the false-positive fix: `commitgate`/`hook`/`gate`
     are LIVE here, not orphaned).
2. **Invokers** (cross-surface): scan `config/skills` + `.claude/skills`,
   `.github/workflows`, `scripts/`, `.claude/settings.json`, and internal Go
   dispatch (`exec.Command`, `dispatchVerb("<verb>")`) for who calls each entry
   point. Classify "agent-invoked-per-documented-skill" as LIVE.
3. **Data edges:** ledger kinds (`internal/ledger`) and store tables
   (`internal/db`) each path produces vs consumes — so a producer→consumer cluster
   that is wholly un-invoked (the `ci_result` stage shape → `Audit`) is flagged.
   Distinguish payload **shapes** under one kind (the `ci_result` gate-vs-stage
   split was decisive — a kind-level check would have mis-judged it).
4. **Backbone:** reuse the local code index (`code search`/`symbol`) for
   symbol→`file:line`; the new layer is the invocation/data graph over it.
5. **Orphan rule:** no system invoker **AND** produced data consumed only by
   un-invoked paths → orphan, with a confidence level + the evidence trail.

**Approach validated.** The method reproduced the manual N1 trace (evidence =
confirmed dead) and resolved every rough-grep candidate to a definite dead/live
verdict, including the two subtleties a naive tool would miss: hook-wired commands
read as LIVE, and one ledger kind splits into a dead and a live cluster by payload
shape. `satellites code map` (sty_8834343c) is clear to build to this spec.
