# Satellites V5 — Architecture

Status: design commit, 2026-05-20. Authoritative reference until V5's substrate ships and the project's `intent_body` is populated from this file.

## Positioning

Satellites V5 is a process substrate that **attaches to whatever primary interface the operator uses** — Claude Code, Warp, Codex, Gemini CLI. It is not a terminal, agent orchestrator, or code editor. It owns the *authored process* layer: the slot Linear/Jira fills with documentation and Warp Oz omits entirely.

One sentence:

> Satellites is the authored-process substrate that turns agent orchestrators into governed workflows — definitions, tooling, and enforceable absolutes, attached to whatever primary interface you use.

## Core thesis: framework, not definition

V5 ships **typed empty slots**. Operators fill them. The substrate enforces structure (gates exist, verdicts are signed by the right role, evidence is referenced) but stays mute on content. There is no built-in `security_review`, no built-in `architecture_review`, no opinion about "the right way" to review. Operators declare what they want; the substrate enforces what they declared.

This is the discipline V3 (in-session, fast but no audit) and V4 (dispatch-via-`claude -p`, isolated but slow and content-coupled) both missed. V3 tangled process into the executor; V4 tried to encode it in substrate documents. V5 separates them: process lives in operator-authored reviewer configs; substrate hosts and enforces.

## Four primitives

| | Owner | Content |
|---|---|---|
| **story** | operator | the requirement, AC, intent |
| **tools** | operator | skills, practices, abilities available to do the work |
| **review** | operator-configured agent | reads story + ACs + evidence; decides "done" |
| **evidence** | substrate (ledger) | append-only record of what happened |

That's the full vocabulary. No `verdict-type`, no `gate-declaration`, no `process-declaration` documents. The reviewer's authored config *is* the policy.

## Review loop

Process = the conversation between the session agent (in-session executor) and N parallel narrow review agents.

For each reviewer attached to a story:

1. Session agent signals "ready for review."
2. Reviewer agent reads ledger + its config + the story.
3. Reviewer emits **pass** or **findings** (concrete, evidence-referenced).
4. On findings: session agent reads findings → fixes → emits fresh ledger evidence → reviewer re-runs.
5. Repeat until pass or circuit-breaker trips.

Reviewers run in parallel when they have no shared mutable state. The session agent serialises fixes; the next round runs all reviewers against the new state. Story close gates on all reviewers passing in the same iteration.

**The loop is evidence-mediated, not conversation-mediated.** Each pass, the reviewer re-reads the ledger fresh; the session agent reads only the latest findings. Neither side carries cumulative chat history. This keeps context bounded over multi-iteration reviews and keeps reviewer judgments independent.

## Circuit breakers + conflicts

- Max iterations per story-close gate (operator-configurable, default 5).
- Max total reviewer tokens per close gate (cost ceiling).
- On circuit-breaker trip: story stays `review_blocked`; unresolved findings surfaced; human override required.
- Reviewer oscillation (A demands X, B demands not-X) surfaces in the ledger after one round; substrate does not silently retry forever.

## Substrate posture

- **Postgres-backed** (replacing V4's SurrealDB). Mature tooling, predictable read paths. Schema lives at `internal/db/migrations/`; migrations managed by [`golang-migrate`](https://github.com/golang-migrate/migrate) and embedded into the server binary via `go:embed` — single-binary deploy, no separate migration assets to ship. `evidence` is enforced append-only at the DB layer (triggers reject UPDATE/DELETE).
- **Thin MCP + CLI gateway.** Single execution path (CLI verbs) for all primaries. The MCP surface is bootstrap-only: two tools — `document_get` (returns the install schema and any other operator-authored document) and `project_match` (resolves a git remote to a `project_id` so the agent can populate the TOML). Together they're enough for a fresh agent to install the CLI and configure it against the right project; every operational verb is dispatched through that CLI. Gemini/Codex/Warp call the same verbs via Bash → CLI. No second-class primaries.
- **Documents-as-substrate.** Operator-editable artifacts (install schemas, principles, runbooks, rubrics) are *documents* in the store, not bespoke typed verbs. Documents are scoped (`system`, `workspace`, `project`) and versioned (append-only); paired *variables* carry name/value pairs in the same scope model. `document_get` substitutes `{{name}}` placeholders against system-computed variables, then project, then workspace, so a single retrieval covers any operator-authored artifact without inventing a new verb per type.
- **No opinionated substrate content.** Starter packs ship as documentation — labelled examples, optional, not loaded by default. The substrate runs fine with zero authored content.

## Visibility discipline

The substrate owes operators a bounded view of what every agent will see:

- Before any dispatch, the full rendered prompt is inspectable.
- Every chunk of context is labelled with provenance (which workspace principle, which project reviewer config, which story body).
- Render budget is hard-capped. If layered config exceeds the budget, the substrate refuses to dispatch and tells the operator which layer to trim.

The rendered prompt is the source of truth. Markdown-that-references-other-markdown is the anti-pattern. Compose at render time; never chain references the operator can't follow in one step.

## Audit trail

The reviewer ↔ session agent conversation, captured in the ledger, is the audit artifact. Months later, an auditor reads it to understand "what was checked, what was challenged, what evidence was produced." Richer than any pre-designed structured audit format, and free — the ledger already captures it.

## What V5 does not ship

- A user-facing shell or terminal. The operator's interface is their primary.
- An execution sandbox. Sandboxing comes from the primary's harness if needed (Oz, Claude Code subagents, etc.).
- Opinionated process content (contracts, principles, reviewer configs). All operator-authored.
- A "default" workflow. The only built-in story-lifecycle behaviour is the close gate (story close requires passing reviewer verdicts).

## Implementation choices

- **Language**: Go (1.25+, driven by `mark3labs/mcp-go` v0.54's minimum).
- **Binary names**: `satellites-server` (server), `satellites` (CLI).
- **Server boundary**: separate `satellites-server` service. CLI calls into the server (not directly into Postgres) — the server owns substrate invariants; the CLI is a typed client.
- **Auth**: OAuth on the server (one provider minimum). Dev mode boots the server with pre-created `admin` + `user` for local development (disabled in production builds; V4 pattern). CLI authenticates with an api-key by default (`Authorization: Bearer`), and supports an interactive OAuth flow that mints an api-key locally.
- **Testing**: Go-driven integration tests using testcontainers (no docker-compose for tests). Each test section shares one config + setup function; data is injected clean per section to prevent state leakage. Co-located under `tests/integration/`.
- **Local development**: docker-compose lives under `scripts/` for the long-running local stack (server + Postgres). One-command bring-up/teardown/reset. Distinct from the test harness.
- **CLI distribution**: `.version` (at repo root) is the canonical version source — per-binary entries (`satellites.version` + `satellites.build`, `satellites-server.version` + `satellites-server.build`) so the CLI and server can rev independently. Auto-bumped on every commit by the `/commit-push` skill. The release workflow triggers on each push to `main`, reads `.version`, and if no matching `v<satellites.version>` tag exists it builds the platform matrix ((linux, darwin) × (amd64, arm64) for `satellites`; linux/amd64 for `satellites-server`) with **per-binary ldflag injection** — each binary self-reports its own version. Asset filenames include the binary's own version (`satellites-v0.0.3-linux-amd64`, `satellites-server-v0.0.4-linux-amd64`). The release tag is the canonical operator-facing version (`v<satellites.version>`); `gh release` notes list both per-binary versions. The MCP `document_get(name=satellites_mcp_install, scope=system)` call returns the install schema — templated `install.download_url` resolves against the server-side `{{cli_version}}` ldflag stamp + the caller's `{{os}}`/`{{arch}}` — pointing at the GitHub "latest release" alias.
- **State directory**: `.satellites/` holds all satellites working state per project — binary, config, logs, per-task artifacts. Listed in `.gitignore`. The substrate-attachment surface lives entirely inside this directory.
- **System seed registry**: agent-facing prose that ships with the binary (install schema, MCP load-context, system-variables taxonomy, any future reviewer/summary skeleton) lives flat under `config/documents/` as markdown and is embedded into `satellites-server` via `go:embed`. Each file declares its identity (`scope`, `name`, `tags`) in YAML frontmatter; the reconciler reads scope from frontmatter, not from the directory layout. On every boot, the embedded bytes are reconciled against the `system_seeds` table (`name`, `body`, `embedded_hash`, `applied_at`) — missing rows are inserted, drifted rows are replaced and `applied_at` is advanced, matching rows are a zero-write no-op. Reconciliation also mirrors the body into the `documents` table at scope=`system` so `document_get` remains the read API. No verb writes to `system_seeds` and the documents store rejects scope=`system` writes from non-seed callers (`ErrScopeReadonly`); the only path that mutates either is the boot reconciler running against immutable embed.FS bytes. Changing a system seed therefore requires editing the markdown in `config/documents/` and shipping a binary release — the rev couples the prose to the version of the server, making it structurally impossible for "which prompt is running where" to drift across environments. Operators audit the registry via the read-only `system_seeds_list` verb.

## Predecessors

- **V3**: `github.com/bobmcallan/satellites-v3` — in-session orchestration; fast but no audit isolation.
- **V4**: `github.com/bobmcallan/satellites-v4` — dispatch-via-`claude -p`; isolated executor + reviewer but slow and content-coupled. Substrate project record (`proj_7a62aedb`) hard-deleted on 2026-05-20; 317 stories exported to `/home/bobmc/development/satellites-v4-stories.md` for reference.
