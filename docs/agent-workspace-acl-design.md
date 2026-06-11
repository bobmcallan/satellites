# Agent workspace ACLs + sibling-project awareness — design recommendation

Spike output for `sty_d6e891fd` (`epic:discovery-workspace-rbac`, parent decision #1 WIP
half). **Status: recommendation, pending operator ratification.** Implementation is a
follow-up story, not this spike.

## Problem

An agent working in a client workspace (e.g. a `discovery` agent) needs two things together:

1. **Cross-project ACLs** — it may *reference* (read-across) a sibling project (e.g. read
   the `code` project's context) but **never change** it. Its own project is read/write per
   membership; siblings are read-only.
2. **Awareness** — it must *know the siblings exist*, plus each one's **type**
   (discovery/code/testing) and **responsibilities**, or it won't know to look. Awareness
   without an ACL is unsafe (it tries to act); an ACL without awareness is unused.

## Recommendation

### Q1 — Grant representation: reuse `project_members` at role `read`

Do **not** add a cross-project ACL table. The existing model already expresses exactly this:

- Read-across = a `project_members` row for the agent's identity on the **sibling** project
  with role **`read`** (`project_member_add {project: <sibling>, user: <agent>, role: read}`).
- `internal/verb/authz.go:effectiveProjectRole` already grants `read` via the explicit-member
  path, and **"no write-across" is automatic** — `read` < `write` < `admin`, so a read member
  cannot upsert/delete or admin the sibling. The document `authorizeWrite` chokepoint denies it.
- No migration, no new verb — allocation is the existing `project_member_*` surface.

**Limitation to flag (the real open item):** an api-key agent's grants ride its *minting
user's* identity today (`auth.FromContext`), so an agent minted by a workspace-admin inherits
admin everywhere — read-across can't be demonstrated as a *restriction* on such an agent. The
clean fix is a **dedicated agent principal** (an identity distinct from the minting human,
whose grants are only its explicit `project_members` rows + its bound project). That is the
one piece worth building; everything else is configuration. Recommend scoping the follow-up
implementation story around the agent-principal + the awareness injection (below).

### Q2 — Responsibilities & type: reuse `description` + derived `non_repo`

No new project `type` enum for the prototype:

- **Responsibilities** = the project `description` (manager-authored, already editable via
  `project_update`).
- **Repo vs document** = the derived `non_repo` flag (`sty_88a76023`).
- **discovery/code/testing** as a first-class type can come later if needed; for now the
  manager states it in the description ("discovery — document collection", "code — the build
  repo", "testing — QA"). Keeps the surface at zero new fields.

### Q3 — Awareness delivery: extend the session-init context, scoped to grants

The resident session-init context is already injected by the `satellites hook context`
SessionStart hook (the always-context / load-context set). Extend it with a **workspace
topology** block:

- List **only the projects the agent has a grant on** (its own + any read-across siblings) —
  the block must mirror the ACL, never leak a project the agent can't reference.
- Per project: `name`, `non_repo`, `description` (responsibilities), and `access level`
  (read / write).
- Re-anchored per `document_get` like the rest of the resident set.

This makes awareness a *view of the grants*, so the two halves (ACL + awareness) cannot drift.

### Q4 — Reconcile with the inherent MCP/code boundary

No conflict. MCP exposes **documents/stories only, never source** (`epic:discovery-delivery`
§2). So a discovery agent's "read code context" **over MCP** = reading the `code` project's
*documents and stories* (its design docs, stories, decisions) — exactly what a read-across
`project_members` grant permits and what MCP serves. **Source code stays CLI-only by
construction.** Broader source read (an agent that reads the actual repo) is a **runner-side
(CLI) execution** concern, designed in the tasks-runner epic, not here.

## Build surface for the follow-up implementation story

- **Config (zero/low code):** allocate read-across via the existing `project_member_add` at
  role `read`; responsibilities via `description`.
- **Code (the real work):** (a) a **dedicated agent principal** so grants don't ride the
  minting human; (b) the **workspace-topology block** in the session-init context, scoped to
  the agent's effective grants.
- **Out of scope:** a project `type` enum; runner-side source read; any new ACL table.

## Decision needed from the operator (ratify)

1. Accept "read-across = `project_members` role `read`" (reuse, no new table)? **Recommended: yes.**
2. Accept "responsibilities = `description`, no `type` field for the prototype"? **Recommended: yes.**
3. Accept the **dedicated agent-principal** as the one code item, paired with the
   awareness-injection, as the follow-up implementation story? **Recommended: yes.**
