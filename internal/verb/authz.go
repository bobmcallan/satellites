// Project-scoped authorization (epic:user-admin, sty_c96cc77f). This is the
// single resolver every project-scoped verb consults; it replaces the prior
// "workspace membership implies project access" behaviour stubbed at
// authorizeListScope (sty_2fa6f087).
//
// Capability matrix (project scope):
//
//	read  — view the project + its stories/docs       (document_get/list/count, project_get)
//	write — create/update stories & documents at scope (document_upsert/delete)
//	admin — write + manage the project's members/settings (project_update)
//
// effectiveProjectRole resolves the caller's role as the MAX of:
//   - global admin (users.role = admin)                    ⇒ admin
//   - workspace owner/admin of the project's workspace     ⇒ admin
//   - an explicit project_members row                      ⇒ that role
//   - an api-key bound to THIS project (executor/runner)   ⇒ write
//   - otherwise                                            ⇒ none ("")
//
// See docs/project-rbac.md.

package verb

import (
	"context"
	"fmt"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/workspace"
)

// projectRoleRank orders the project roles so callers can express "≥ write".
// none < read < write < admin.
func projectRoleRank(role string) int {
	switch role {
	case project.RoleAdmin:
		return 3
	case project.RoleWrite:
		return 2
	case project.RoleRead:
		return 1
	}
	return 0
}

func rankToProjectRole(rank int) string {
	switch {
	case rank >= 3:
		return project.RoleAdmin
	case rank == 2:
		return project.RoleWrite
	case rank == 1:
		return project.RoleRead
	}
	return ""
}

// effectiveProjectRole returns the caller's effective role on projectID,
// resolving the project's workspace via the project store. Use this from
// callers that only know the project id (e.g. project_get/update).
func effectiveProjectRole(ctx context.Context, projectID string) string {
	return effectiveProjectRoleWS(ctx, "", projectID)
}

// effectiveProjectRoleWS is the caller's ACTUAL project role, capped by any
// requested agent-role downscope on ctx (sty_3a1374b5). The cap can only
// ATTENUATE: effective = min(actual, cap.Role), and a project outside
// cap.Projects (when the list is non-empty) resolves to "". No cap ⇒ the actual
// role, unchanged — so non-agent (human/session) callers are unaffected.
func effectiveProjectRoleWS(ctx context.Context, wsID, projectID string) string {
	actual := actualProjectRoleWS(ctx, wsID, projectID)
	cap, ok := auth.AgentRoleFromContext(ctx)
	if !ok {
		return actual
	}
	if len(cap.Projects) > 0 && !sliceContains(cap.Projects, projectID) {
		return "" // project outside the requested allow-list
	}
	if projectRoleRank(cap.Role) < projectRoleRank(actual) {
		return cap.Role // min(actual, requested)
	}
	return actual
}

func sliceContains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// actualProjectRoleWS returns the caller's effective project role
// (admin|write|read|"") as the max over every grant path, BEFORE any agent-role
// cap. wsID, when non-empty, is the project's known workspace (e.g. a document
// key's workspace_id) — authoritative for the workspace owner/admin inheritance
// check, so it works even when no projects row exists yet. When wsID is "",
// the workspace is resolved from the project store. Defensive against unwired
// stores (a missing store simply contributes no grant from that path).
func actualProjectRoleWS(ctx context.Context, wsID, projectID string) string {
	if projectID == "" {
		return ""
	}
	u := auth.FromContext(ctx)
	caller := ""
	if u != nil {
		if u.Role == auth.RoleAdmin {
			return project.RoleAdmin // global admin is admin everywhere
		}
		caller = u.ID
	}

	best := 0

	// Workspace owner/admin of the project's workspace ⇒ project admin.
	if caller != "" && workspaceStore != nil {
		ws := wsID
		if ws == "" && projectStore != nil {
			if pj, err := projectStore.GetByID(ctx, projectID); err == nil {
				ws = pj.WorkspaceID
			}
		}
		if ws != "" {
			if role, err := workspaceStore.GetRole(ctx, ws, caller); err == nil {
				if role == workspace.RoleOwner || role == workspace.RoleAdmin {
					return project.RoleAdmin
				}
			}
		}
	}

	// Explicit project_members grant.
	if caller != "" && projectStore != nil {
		if role, err := projectStore.GetRole(ctx, projectID, caller); err == nil {
			if r := projectRoleRank(role); r > best {
				best = r
			}
		}
	}

	// An api-key bound to THIS project maps onto write (executor/runner).
	if auth.APIKeyProjectFromContext(ctx) == projectID && auth.APIKeyRoleFromContext(ctx) != "" {
		if w := projectRoleRank(project.RoleWrite); w > best {
			best = w
		}
	}

	// Readonly mount: a member of any workspace that readonly-mounts this
	// project gets read (capped). The mount never elevates to write — write
	// needs the home owner/admin, a project_members row, or an api-key binding —
	// so "no write through a mount" holds by construction (sty_1ede3853).
	if caller != "" && workspaceStore != nil && best < projectRoleRank(project.RoleRead) {
		if ok, err := workspaceStore.CallerHasMountAccess(ctx, projectID, caller); err == nil && ok {
			best = projectRoleRank(project.RoleRead)
		}
	}

	return rankToProjectRole(best)
}

// effectiveProjectRoleAtLeast reports whether the caller's effective role on
// projectID is at least want.
func effectiveProjectRoleAtLeast(ctx context.Context, projectID, want string) bool {
	return projectRoleRank(effectiveProjectRole(ctx, projectID)) >= projectRoleRank(want)
}

// enforceProjectScope returns a forbidden error when the caller's effective
// role on projectID (within the known workspace wsID, if any) is below need.
func enforceProjectScope(ctx context.Context, wsID, projectID, verbName, need string) error {
	if projectID == "" {
		return fmt.Errorf("%s: %w: project scope requires project_id", verbName, ErrBadRequest)
	}
	have := effectiveProjectRoleWS(ctx, wsID, projectID)
	if projectRoleRank(have) < projectRoleRank(need) {
		if have == "" {
			return fmt.Errorf("%s: %w: no access to project %s", verbName, ErrForbidden, projectID)
		}
		return fmt.Errorf("%s: %w: project role %q insufficient (need %s)", verbName, ErrForbidden, have, need)
	}
	return nil
}

// canManageWorkspace reports whether the caller is a global admin or a
// workspace owner/admin of wsID — the gate for creating projects in a
// workspace. CLI-local callers (no auth wiring) are handled by the verb's
// own authStore==nil bypass before this is consulted.
func canManageWorkspace(ctx context.Context, wsID string) bool {
	u := auth.FromContext(ctx)
	if u == nil {
		return false
	}
	admin := u.Role == auth.RoleAdmin
	if !admin && workspaceStore != nil && wsID != "" {
		if role, err := workspaceStore.GetRole(ctx, wsID, u.ID); err == nil {
			admin = role == workspace.RoleOwner || role == workspace.RoleAdmin
		}
	}
	if !admin {
		return false
	}
	// Honour an agent-role downscope (sty_3a1374b5): a session capped below admin,
	// or scoped to specific projects, is not acting as a workspace manager — it
	// cannot create projects or grant members.
	if cap, ok := auth.AgentRoleFromContext(ctx); ok {
		if projectRoleRank(cap.Role) < projectRoleRank(project.RoleAdmin) || len(cap.Projects) > 0 {
			return false
		}
	}
	return true
}

// callerIsGlobalAdmin reports whether the caller is a global admin.
func callerIsGlobalAdmin(ctx context.Context) bool {
	u := auth.FromContext(ctx)
	return u != nil && u.Role == auth.RoleAdmin
}

// CallerMeetsMCPRole reports whether the caller's resolved role satisfies the
// required MCP role tier. It is the single resolver the MCP surface consults
// from BOTH the tools/list filter (visibility) and the dispatch gate
// (call-time enforcement), so the two can never diverge. Fail closed: an
// unknown tier, an unauthenticated/unresolvable caller, or an unwired store
// all resolve to false for any non-base tier.
func CallerMeetsMCPRole(ctx context.Context, required string) bool {
	switch required {
	case "":
		return true // base/public surface
	case MCPRoleWorkspaceAdmin:
		// Honour an agent-role downscope (sty_3a1374b5): a session capped below
		// admin, or scoped to specific projects, is not acting on an admin
		// surface — mirror canManageWorkspace so listing and dispatch agree.
		if cap, ok := auth.AgentRoleFromContext(ctx); ok {
			if projectRoleRank(cap.Role) < projectRoleRank(project.RoleAdmin) || len(cap.Projects) > 0 {
				return false
			}
		}
		if callerIsGlobalAdmin(ctx) {
			return true
		}
		u := auth.FromContext(ctx)
		if u == nil || workspaceStore == nil {
			return false
		}
		ok, err := workspaceStore.HasAdminRole(ctx, u.ID)
		return err == nil && ok
	default:
		return false // unrecognised tier — fail closed
	}
}

// canPatchWorkspace reports whether the caller may patch ws's mutable fields
// (name/description) via workspace_upsert: a platform-admin, or the workspace
// owner (owner_user_id). Delegated workspace 'admin' members are intentionally
// NOT permitted here — workspace settings are owner-or-platform-admin
// (sty_7d7f3037). An agent-role downscope capped below admin is never an owner.
func canPatchWorkspace(ctx context.Context, ws workspace.Workspace) bool {
	if cap, ok := auth.AgentRoleFromContext(ctx); ok {
		if projectRoleRank(cap.Role) < projectRoleRank(project.RoleAdmin) || len(cap.Projects) > 0 {
			return false
		}
	}
	if callerIsGlobalAdmin(ctx) {
		return true
	}
	caller := auth.FromContext(ctx)
	return caller != nil && ws.OwnerUserID != "" && ws.OwnerUserID == caller.ID
}

// isWorkspaceMember reports whether the caller is a global admin or any member
// of wsID. Gates read-only workspace surfaces (e.g. the member roster).
func isWorkspaceMember(ctx context.Context, wsID string) bool {
	if callerIsGlobalAdmin(ctx) {
		return true
	}
	u := auth.FromContext(ctx)
	if u == nil || workspaceStore == nil || wsID == "" {
		return false
	}
	_, err := workspaceStore.GetRole(ctx, wsID, u.ID)
	return err == nil
}

// canListWorkspaceProjects reports whether the caller may list the projects of
// wsID: a global admin (any workspace), or any member of wsID. A non-admin
// caller must name a workspace they belong to (no unbounded cross-workspace
// listing).
func canListWorkspaceProjects(ctx context.Context, wsID string) bool {
	if callerIsGlobalAdmin(ctx) {
		return true
	}
	u := auth.FromContext(ctx)
	if u == nil || workspaceStore == nil || wsID == "" {
		return false
	}
	_, err := workspaceStore.GetRole(ctx, wsID, u.ID)
	return err == nil
}
