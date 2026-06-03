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
| `satellites exec <verb>` | Direct verb dispatch — JSON in, JSON out, byte-identical to the MCP call. The single execution path. | DONE. |
| `satellites seed` | Push file-based seeds to the substrate. | DONE. |
| `satellites story review` | Run a story's reviewer gate client-side (`claude -p` against the worktree); the reviewer enacts the status transition. | DONE. |
| `satellites techdebt review` | The technical-debt pre-commit gate — build + unit + integration reconciled against the quarantine register, fail closed on an unregistered red. | DONE. |
| `satellites surface check` | The command-surface drift gate — fail closed when a live command is not named in this document. Keeps the reference docs in step with the CLI as `update` ships changes. | DONE. |

When `update` adds, renames, or removes a command, reconcile this section in the
same change — `satellites surface check` blocks the commit until it matches.

## See also

- The verbs above — `satellites auth`, `satellites update`, `satellites deploy` —
  and the baseline-project-process work.
- [[project-substrate-inclusion]], [[no-new-mcp-verbs]], [[process-as-configuration]].
- document:system/satellites_client_install (the install schema the
  bootstrap acts on), [[project-substrate-inclusion]], [[no-new-mcp-verbs]].
