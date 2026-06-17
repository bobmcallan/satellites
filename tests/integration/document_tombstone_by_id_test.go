//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestDocumentTombstoneByID is the DOGFOOD for sty_52f62518 (epic-order:2.5.1):
// a project-scoped document stores workspace_id NULL since mig-0041, so deleting
// it via a key reconstructed from the stored row fails Key.Validate (which
// requires workspace_id for ScopeProject) — the broken id-delete door. The
// id-addressed TombstoneByID removes the located row by primary key regardless.
func TestDocumentTombstoneByID(t *testing.T) {
	env := testbootstrap.SetUpWithServer(t)
	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	docStore := document.New(env.DB)

	ctx := context.Background()
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)

	admin, err := env.Store.GetUserByID(ctx, "usr_dev_admin")
	if err != nil {
		t.Fatalf("get admin: %v", err)
	}
	ws, err := wsStore.Create(ctx, admin.ID, "tomb-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "tomb-pj", OwnerUserID: admin.ID}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	// Seed a project-scoped document. The caller supplies workspace_id (passing
	// Key.Validate); the store keys it by (project_id, name) with workspace_id NULL.
	doc, _, err := docStore.Upsert(ctx, document.UpsertInput{
		Key:       document.Key{Scope: document.ScopeProject, WorkspaceID: ws.ID, ProjectID: pj.ID, Name: "tomb-target"},
		Type:      "document",
		Body:      "to be removed",
		CreatedBy: admin.ID,
	}, now)
	if err != nil {
		t.Fatalf("seed upsert: %v", err)
	}

	// The bug: deleting via a key reconstructed from the stored row (workspace_id
	// empty, as every project row stores it) fails Key.Validate.
	reconstructed := document.Key{Scope: document.ScopeProject, ProjectID: pj.ID, Name: "tomb-target"}
	if _, _, derr := docStore.Delete(ctx, reconstructed, admin.ID, false, now); !errors.Is(derr, document.ErrScopeMismatch) {
		t.Fatalf("Delete(reconstructed-key) = %v, want ErrScopeMismatch (the broken id-delete door)", derr)
	}

	// The fix: TombstoneByID removes the located row by primary key.
	tomb, v, err := docStore.TombstoneByID(ctx, doc.ID, admin.ID, now)
	if err != nil {
		t.Fatalf("TombstoneByID: %v", err)
	}
	if tomb.Status != string(document.StatusDeleted) || v.Status != document.StatusDeleted {
		t.Fatalf("tombstone status = %q / version %q, want deleted", tomb.Status, v.Status)
	}

	// The row leaves the active set: its row-level status is deleted.
	got, err := docStore.GetByID(ctx, doc.ID)
	if err != nil {
		t.Fatalf("GetByID after tombstone: %v", err)
	}
	if got.Status != string(document.StatusDeleted) {
		t.Fatalf("row status after tombstone = %q, want deleted", got.Status)
	}

	// A missing id returns ErrNotFound, not a panic or a silent success.
	if _, _, err := docStore.TombstoneByID(ctx, "doc_missing0", admin.ID, now); !errors.Is(err, document.ErrNotFound) {
		t.Fatalf("TombstoneByID(missing) = %v, want ErrNotFound", err)
	}
}
