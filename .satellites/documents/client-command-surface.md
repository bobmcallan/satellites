---
name: client-command-surface
type: document
tags: [area:cli, kind:design]
---

# Client command surface — verb map, bootstrap-as-skill, global-app packaging

The decision of record for the satellites **client** as it becomes a
globally-installed app (like the `claude` CLI), not a per-repo
`.satellites/satellites` binary. Each behaviour is one deterministic
verb; first-run is a thin orchestration skill over those verbs. Holds to
[[no-new-mcp-verbs]] (these are CLI subcommands) and
[[process-as-configuration]] (this is a document, not a Go branch).

## 1. Verb map — what each verb owns

| Verb | Owns | State |
| ---- | ---- | ----- |
| `satellites install` | Place the binary: download the release artifact, verify its `sha256`, put it in place. `--global` (default) → `~/.local/bin/satellites` on PATH; `--local` → the in-repo `.satellites/satellites`; `--version` pins, `--force` overwrites, refuse-clobber otherwise. The `curl \| sh` bootstrap (`scripts/install.sh`), shape of `claude install`. | DONE. |
| `satellites auth` | **Identity.** Human-in-the-loop browser login (loopback OAuth2 + PKCE) → an executor api-key in the user-level credential store (`~/.config/satellites/credentials.toml`, 0600, keyed by server_url). The bootstrap JWT is used once and discarded. | DONE — DONE. |
| `satellites project match` | **Project selection.** Resolve a `project_id` from the repo's git remote; the result is written to the repo's `.satellites/satellites.toml`. | DONE. |
| `satellites update` | **Binary refresh.** Self-update the global binary in place from the release channel, checksum-verified; no-op when current. | DONE. |
| `satellites deploy` | **Substrate → local pull.** Reconcile `.claude/skills/` against the repo's project scope (install/update/remove by identity stamp, never clobber an edited or operator-authored skill). **Pull-only** — it does not push. | DONE. |
| `satellites {document,skill,principle} upload` | **Local → substrate push.** Validate + upsert the repo's `config/` sources to the substrate, project/workspace-bound. A deliberate operator/agent verb, invoked as a prompt — never coupled into a bootstrap or session-start step. | DONE. |
| `satellites skill publish <name>` | **Local → library promotion.** Publish one `.satellites/skills/` skill into the shared library under this repo's publisher namespace (identity = publisher project + name), gated by the same strict content review as upload, provenance-stamped (publisher, repo URL, commit). Headless (API-key auth) so a CI step publishes on merge; `--dryrun` previews identity/version/provenance without dispatching. | DONE. |
| `satellites skill search <term>` | **Library discovery.** Match name/description across the shared library (every publisher) plus the caller's accessible scopes; one row per (publisher, name) with publisher, scope, version, headline — competing offerings compare at a glance. | DONE. |
| `satellites skill adopt <publisher>/<name>` | **Fork-and-own consumption.** Copy a library skill into `.satellites/skills/<name>.md` with a `forked-from` provenance stamp in frontmatter; the upstream link is severed — it becomes an ordinary project skill on the normal upload/review path. Overwriting an existing local skill needs `--force`. | DONE. |
| `library_pins` (satellites.toml) | **Use-as-is consumption.** A repo pins library skills by `"<publisher>/<name>"`; `skill sync` materialises them into `.claude/skills/` at lowest precedence (a same-named project/local skill wins), stamps publisher + version + document identity, lands upstream publishes on the next sync, and removes the copy when the pin goes. | DONE. |
| **bootstrap skill** | **First-run orchestration only.** Detects what is already done and delegates to the verbs above. Holds none of their logic. | See §2. |

Single-source rule: a dynamic step lives in exactly one verb. `auth` owns
identity, `update` owns the binary, `deploy` owns the substrate→local skills
pull, the `{document,skill,principle} upload` verbs own the local→substrate
push, `project match` owns project resolution. The bootstrap skill *sequences*
them; it never re-implements a step. Push and pull are deliberately split:
bootstrap/session-start only ever *pulls* (a client has no `config/` to push).

## 2. Bootstrap-as-skill — enumerated branches

First-run is a **skill** (process, not code) that runs guarded delegations.
Each branch is `is-it-already-done? → skip : run-the-verb`:

| Branch | Guard (skip when…) | Else delegate to |
| ------ | ------------------ | ---------------- |
| install | the `satellites` binary is on PATH | `satellites install` |
| auth | a credential for this `server_url` exists in the credential store | `satellites auth` |
| project | the repo TOML carries a `project_id` | `satellites project match` → write `project_id` |
| sync | `.claude/skills/` reconciles clean against the substrate | `satellites deploy` |

The skill is authored once `satellites install` lands (its first branch
needs the installer). Until then the load-context bootstrap (the
`auth_bootstrap` block of document:system/satellites_client_install) plus
`satellites auth` + `satellites project match` cover first-run; this entry
records the target shape so the skill, when written, only orchestrates.

## 3. Global-app packaging decisions

- **Binary location — global, user-level, on PATH.** `~/.local/bin/satellites`
  (the `claude` model), NOT the per-repo `.satellites/satellites`. The
  per-repo `.satellites/` directory remains for worktrees, logs, and the
  **non-secret** `satellites.toml`; the executable itself is shared across
  every repo on the machine.
- **Project selection — per-repo TOML, identity per-server.** The global
  binary, run inside a repo, reads that repo's `.satellites/satellites.toml`
  for `project_id` (resolved once via `satellites project match`). *Which*
  project a call is about comes from the repo; *who* the caller is comes
  from the per-server credential (credential store); *whether* the caller
  may act is enforced server-side by workspace membership. One credential
  per server serves every project the user can access — see the
  `satellites auth` identity model.
- **Update target — the global binary, in place.** `satellites update`
  refreshes the on-PATH binary, verified against the published checksum;
  `install` places it, `update` refreshes it. Neither touches a repo's
  `.satellites/` (which is per-project working state, not the executable).

## 4. Operational + gate commands

Beyond the identity/binary/substrate verbs of §1, the client carries a few
operational and gate commands. They are part of the surface and are kept here
so the `satellites surface check` gate stays green ([[broken-windows]]):

| Command | Owns | State |
| ------- | ---- | ----- |
| `satellites version` | Print the binary's build info. A non-release build reports a git-derived `0.0.0-dev+<sha>` version (never bare `dev`). | DONE. |
| `satellites status` | Read-only health/identity over the `system_status` MCP verb — server version/commit/build, substrate DB reachability (ok + latency), process uptime, and the local CLI build for a quick skew check; `--json` for the raw response. `system_status` is the sanctioned introspection exception to [[no-new-mcp-verbs]]. | DONE. |
| `satellites exec <verb>` | Direct verb dispatch — JSON in, JSON out, byte-identical to the MCP call. The single execution path. | DONE. |
| `satellites seed` | Push file-based seeds to the substrate. | DONE. |
| `satellites changelog upsert` | Create or update a changelog (release-notes) entry — composes the existing `document_upsert` verb to write a system-scope `type:changelog` document (the `/changelog` portal page reads these), with `--service`/`--from`/`--to`/`--effective-date` as tags and the body from `--body`/`--file`/stdin. The runtime write path the changelog collapse (epic:satellites-backbone) pointed at but left unwired; no new MCP verb ([[no-new-mcp-verbs]]). The server permits this one system-scope write only for a global admin off the MCP surface. | DONE. |
| `satellites story review` | Run a story's reviewer gate client-side (`claude -p` against the worktree); the reviewer enacts the status transition. | DONE. |
| `satellites surface check` | The command-surface drift gate — fail closed when a live command is not named in this document. Keeps the reference docs in step with the CLI as `update` ships changes. | DONE. |
| `satellites init` | Make a repo ready for satellites, idempotently — ensure `.satellites/`, a `satellites.toml`, the PreToolUse START-door + advisory hooks in `.claude/settings.json`, a `.gitignore` managed block for `.satellites/` local state, and the gateless baseline workflow in `.satellites/workflows/` (create-if-absent). Re-run when hooks change; existing files/settings are preserved. | DONE. |
| `satellites rebase [--workflows\|--hooks]` | Reset a repo's satellites governance to the clean baseline (epic:satellites-backbone). Archives the current `.satellites/workflows/` reversibly and scaffolds the gateless baseline, and reconciles `.claude/settings.json` hooks to the current `init` set (the path that closes a stale settings.json missing a hook). With no flag it does both; refresh skills afterwards with `satellites skill sync`. | DONE. |
| `satellites hook <gate\|access\|prompt>` | The handlers Claude Code invokes from `.claude/settings.json` (installed by `init`). `gate` is the START door — blocks file edits until a story is engaged; `access`/`prompt` are advisory engage reminders. Read the engagement store. | DONE. |
| `satellites context show <story>` | Render the COMPLETE delivered context for a story — load-context, skills index, principles ride-along, the story's `## Workflow`, and the envelope + recent ledger — each labelled by source + size (bytes/~tokens), with a grand total. Operator-facing, out of band; the delivered-context baseline (epic:qa-observability). `review` adds structural context-conflict checks; `curate` trims the principles ride-along by option. | DONE. |
| `satellites workflow design <story>` | Design a story's `## Workflow` from its requirement — an isolated `claude -p` subagent proposes candidate state machines (validated lifecycle + gate-skill resolution, each justified); `--apply N` writes the chosen one into the story. Fail-closed. Out of band (epic:qa-observability). | DONE. |
| `satellites workflow show <name\|story-id> [--dot]` | Render a workflow definition — a client-dir workflow / materialised `kind:workflow` skill by name, or a story's embedded `## Workflow` by `sty_` id. Default a readable text table (states/actors/commands, transitions with gates, on-edges, bounds, exhaustion targets); `--dot` emits Graphviz DOT (actors shape nodes, fail edges dashed with their ×N bound, exhaustion edges dotted, checkpoint gates as side nodes) for `dot -Tsvg`. DOT is export-only, never the execution format. Read-only. | DONE. |
| `satellites workflow embed <story-id> [--json]` | Resolve the story's governing workflow by `applies_to ↔ category` over `.satellites/workflows/` (client-dir, preferred) then the materialised `kind:workflow` skills, stamp its `## Workflow` yaml into the story body, and print the workflow + the next gate. The config-driven story-start step that replaces hand-copying the workflow from a skill; idempotent (only writes on drift), fail-closed when no workflow covers the category (epic:client-dir-separation order-2). | DONE. |
| `satellites workflow check` | The process-drift validator (read-only, fail closed on any blocking finding): reconciles the defined process — client-dir workflows + the materialised skill tree + the project's stories — against itself (unusable-skill, workflow-lifecycle, orphan/missing-gate, ungoverned-story, ambiguous-governance, first-gate-shallow, …). Named by the workflow-drift commit gate. | DONE. |
| `satellites evidence <show\|ci>` | The durable QA-evidence trail per story (epic:qa-observability). `show` lists a story's captured gate runs + CI outcomes from the per-repo store (`--json`); `ci <story> --stage <test\|release\|deploy> --result <success\|failure>` records a CI outcome to the store and the server ledger (`ci_result` row). Gate runs are captured automatically by `story review`. Out of band — never the executor's turn. | DONE. |
| `satellites story get <id>` | Server-side story read — status, state actor (derived from the story's embedded `## Workflow`), category, priority, tags, parent, headline. The server complement of `satellites work status` (the local live-engagement view). Read-only. | DONE. |
| `satellites ledger list <story-id> [--limit N]` | Server-side ledger read — a story's recent rows (transitions annotated from → to, gate verdicts, summaries), newest last. The first ledger read path outside the portal. Read-only; appends stay with the gate chain. | DONE. |
| `satellites workspace objective <id>` | Synthesize (or refresh) a workspace's objective from its document corpus (epic:workspace-engagement) — task-as-configuration with a pluggable executor. `--executor gemini` (default) dispatches the `workspace_objective_generate` verb (Gemini server-side); `--executor claude` runs `claude -p` locally over the same task prompt and writes the objective back. The objective is stored as the workspace document `objective` and rendered on the workspace page. | DONE. |
| `satellites semantic-search <query>` | Search a workspace's document corpus by semantic similarity (epic:workspace-engagement). Server-side verb (`semantic_search`, also an MCP tool): embeds the query with the corpus embedder, cosine-ranks the workspace's chunks, returns top-k results with document provenance + score. `--workspace` (defaults to the configured project's workspace), `--limit`. Requires server-side embeddings (`GEMINI_API_KEY`); degrades to an empty noted result otherwise. | DONE. |
| `satellites code <index\|search\|symbol>` | The in-client code symbol index (epic:code-index). `index` builds a per-repo `.satellites/index.db` (pure-Go SQLite + FTS5) of symbols from the repo; `search <query>` lists matching symbols (name, kind, file:line) via FTS5 prefix + substring fallback; `symbol <name>` prints the exact source slice by stored byte offsets. CLI-only, no MCP — the agent searches symbols instead of reading whole files. The store/schema/search are language-neutral; the spike ships a Go `go/ast` extractor and the order:2 generalisation swaps in CGo-free WASM tree-sitter for any language behind the same seam. | DONE (spike: Go-only parse). |

When `update` adds, renames, or removes a command, reconcile this section in the
same change — `satellites surface check` blocks the commit until it matches.

## See also

- The verbs above — `satellites auth`, `satellites update`, `satellites deploy` —
  and the baseline-project-process work.
- [[project-substrate-inclusion]], [[no-new-mcp-verbs]], [[process-as-configuration]].
- document:system/satellites_client_install (the install schema the
  bootstrap acts on), [[project-substrate-inclusion]], [[no-new-mcp-verbs]].
