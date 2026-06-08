# Project-scoped RBAC

Server-side authorization for project-scoped operations (epic:user-admin,
sty_c96cc77f). Authorization is server-side truth — hiding a control in the UI
is not a permission; every project-scoped read/write/admin operation re-checks
the caller's effective role on the server.

## Roles & capability matrix

Project roles, lowest to highest: `read` < `write` < `admin`.

| capability | role required | operations |
|------------|---------------|------------|
| view the project + its stories/docs | **read** | `document_get`, `document_list`, `document_count`, `project_get` |
| create/update stories & documents at project scope | **write** | `document_upsert`, `document_delete` |
| manage the project's members & settings | **admin** | `project_update` |

`project_create` is gated at the workspace level: a workspace owner/admin (or
global admin) may create projects in that workspace.

## effectiveProjectRole

`internal/verb/authz.go:effectiveProjectRole` resolves a caller's effective role
on a project as the **maximum** over every grant path:

1. **Global admin** (`users.role = admin`) ⇒ `admin` on every project.
2. **Workspace owner/admin** of the project's workspace ⇒ `admin` on every
   project in it (the GitHub "org owner is admin on every repo" rule).
3. **Explicit `project_members` row** ⇒ that role.
4. **API-key bound to the project** (executor/runner key whose `project_id`
   matches) ⇒ `write`. This keeps CI/runner callers authoring at project scope
   without granting project-admin. The binding is surfaced onto the request
   context by the auth middleware (`auth.APIKeyProjectFromContext`).
5. Otherwise ⇒ none (denied).

The resolver is the single chokepoint the document authorize functions
(`authorizeRead` / `authorizeListScope` / `authorizeWrite`) and the project
verbs consult — replacing the prior "workspace membership implies project
access" behaviour stubbed by sty_2fa6f087.

## CLI-local bypass

In-process CLI invocations with no auth store wired skip the membership/role
checks (the same escape hatch the prior workspace-membership checks used). The
server always wires the auth store, so deployed paths always enforce.
