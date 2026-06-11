# Discovery workspace RBAC — mapping to existing model

The discovery delivery model (`epic:discovery-delivery`, sty_37bb4b78) calls for a
**client workspace** containing `discovery` / `code` / `testing` projects, administered by a
**manager** who allocates people *and agents* to projects, with project-granular isolation
(a person/agent allocated to `discovery` does not thereby reach `code`).

**This requirement is already satisfied** by the project-scoped RBAC shipped in
`epic:user-admin` (`sty_6bc72973`). No new role or verb is needed — this document maps the
discovery vocabulary onto the existing mechanisms so the discovery epics build on them
directly. See `docs/project-rbac.md` for the base model.

## Mapping

| Discovery requirement | Existing mechanism |
|---|---|
| **workspace-manager** (administers the workspace, allocates to projects) | **workspace owner/admin** — admin on *every* project in the workspace (the "org owner is admin on every repo" rule) and the gate on `project_create`. Resolved by `internal/verb/authz.go:effectiveProjectRole`. |
| **allocate a person to a project** | `project_member_add` (+ `project_member_list` / `project_member_update_role` / `project_member_remove`); workspace-level grants via `workspace_member_*`. Driven by the `/settings/people` UI (`internal/server/admin_people.go`). |
| **allocate an agent to a project** | a **project-bound api-key** — `apikey_create` with `project_id`; an api-key-bound caller resolves to `write` on its bound project only (`auth.APIKeyProjectFromContext`). |
| **project isolation** (discovery ⇏ code) | `effectiveProjectRole` returns the **max over grant paths**: global-admin, workspace owner/admin, explicit `project_members` row, project-bound api-key. A non-admin member/agent allocated to one project gets *no* grant on a sibling. |

## Deliberate caveat

A workspace **manager (= workspace owner/admin) is admin on every project by design** — that
is what "manager" means. Project isolation therefore applies to **non-admin** members and to
**per-project api-key bindings**, not to the manager. This matches the discovery model: the
manager sees everything in their client workspace; the people/agents they allocate are
scoped to the specific project.

## Verified (dogfood, satellites workspace `wksp_6f048cd8`)

Projects: `satellites` (`proj_fc7d72d8`, code) + `satellites-discovery` (`proj_ef9fadba`,
document-collection) — two projects under one manager, the managed area.

- `project_member_add {project: proj_ef9fadba, user, role: read}` → the user appears in
  `project_member_list` for **discovery**, and the **code** project's member list stays
  empty — allocation is project-granular. (Row removed afterward; lists returned to empty.)
- The discovery agent key `apk_mcp_1b2b4c92` (role `executor`) is bound to **`proj_ef9fadba`
  only** — an agent allocated to discovery, not code.

## What is NOT covered here

Cross-project **read-across** for agents (a discovery agent reading `code` context,
read-only) is a *different* grant than membership and is designed in
`agent-workspace-acl-and-awareness` (`sty_d6e891fd`) — the WIP half of parent decision #1.
