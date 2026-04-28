package portal

import (
	"testing"

	"github.com/bobmcallan/satellites/internal/auth"
)

// TestPortal_GlobalAdminChip_RendersForCrossWorkspaceAdmin covers AC6:
// when a user is a global_admin AND the active workspace differs from
// any of their memberships, globalAdminChip returns true (template
// renders the chip).
func TestPortal_GlobalAdminChip_RendersForCrossWorkspaceAdmin(t *testing.T) {
	t.Parallel()
	p := &Portal{
		globalAdminEmails: map[string]struct{}{"alice@example.com": {}},
	}
	user := auth.User{ID: "u_alice", Email: "alice@example.com"}
	active := wsChip{ID: "wksp_foreign", Name: "foreign"}
	memberships := []string{"wksp_home"}

	if !p.globalAdminChip(user, active, memberships) {
		t.Errorf("expected chip to render for global_admin acting on foreign workspace")
	}
}

// TestPortal_GlobalAdminChip_HiddenInOwnWorkspace covers the AC6
// hide-case: a global_admin acting inside one of their own memberships
// does NOT render the chip — they are not crossing tenancy.
func TestPortal_GlobalAdminChip_HiddenInOwnWorkspace(t *testing.T) {
	t.Parallel()
	p := &Portal{
		globalAdminEmails: map[string]struct{}{"alice@example.com": {}},
	}
	user := auth.User{ID: "u_alice", Email: "alice@example.com"}
	active := wsChip{ID: "wksp_home", Name: "home"}
	memberships := []string{"wksp_home", "wksp_other"}

	if p.globalAdminChip(user, active, memberships) {
		t.Errorf("expected chip hidden when admin acts inside their own workspace")
	}
}

// TestPortal_GlobalAdminChip_HiddenForNonAdmin covers the AC6 negative
// case: non-admins never see the chip regardless of active workspace.
func TestPortal_GlobalAdminChip_HiddenForNonAdmin(t *testing.T) {
	t.Parallel()
	p := &Portal{globalAdminEmails: map[string]struct{}{}}
	user := auth.User{ID: "u_bob", Email: "bob@example.com"}
	active := wsChip{ID: "wksp_anywhere", Name: "anywhere"}
	memberships := []string{"wksp_other"}

	if p.globalAdminChip(user, active, memberships) {
		t.Errorf("expected chip hidden for non-admin user")
	}
}

// TestPortal_GlobalAdminChip_HiddenForGlobalAdminWithNoActiveWorkspace
// covers a defensive case: when active.ID is empty (e.g. the user has
// no workspace bound yet) the chip stays hidden so we don't surface a
// "GLOBAL ADMIN" badge in the boot/landing path before scoping has
// resolved.
func TestPortal_GlobalAdminChip_HiddenForNoActiveWorkspace(t *testing.T) {
	t.Parallel()
	p := &Portal{
		globalAdminEmails: map[string]struct{}{"alice@example.com": {}},
	}
	user := auth.User{ID: "u_alice", Email: "alice@example.com"}
	active := wsChip{}
	memberships := []string{"wksp_home"}

	if p.globalAdminChip(user, active, memberships) {
		t.Errorf("expected chip hidden when active.ID is empty")
	}
}
