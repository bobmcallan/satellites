//go:build integration

package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestStoreUpsertLibraryTask pins the sty_40baf67d store fix: `satellites task
// publish` routes a published task to document.Store.Upsert at scope:library
// (routesToCreateTask is false there), and that generic path MUST accept
// type:task — it previously rejected it ("unsupported type task"), so publish
// failed. The library copy is a plain versioned row (body+tags) that `task sync`
// materialises back into a real project task. Project tasks still mint via
// createTask, so a type:task at any OTHER scope through this generic path stays
// rejected.
func TestStoreUpsertLibraryTask(t *testing.T) {
	env := testbootstrap.SetUp(t)
	wsStore := workspace.New(env.DB)
	projStore := project.New(env.DB)
	docStore := document.New(env.DB)

	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	ws, err := wsStore.Create(ctx, "", "publisher-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pub, err := projStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "publisher"}, now)
	if err != nil {
		t.Fatalf("create publisher project: %v", err)
	}

	libKey := document.Key{Scope: document.ScopeLibrary, ProjectID: pub.ID, Name: "Codegraph"}
	body := "# Codegraph\n\n## Task\n\nGenerate the repo's high-level codegraph.\n"
	doc, _, err := docStore.Upsert(ctx, document.UpsertInput{
		Key: libKey, Type: document.TypeTask, Body: body, CreatedBy: "usr_dev_admin",
	}, now)
	if err != nil {
		t.Fatalf("library task upsert must succeed (the publish store path): %v", err)
	}
	if doc.Type != string(document.TypeTask) {
		t.Errorf("library task type = %q, want task", doc.Type)
	}

	// Round-trips: a Get on the library key returns the body.
	got, err := docStore.Get(ctx, libKey, document.GetOptions{})
	if err != nil || len(got.Versions) == 0 {
		t.Fatalf("library task get: err=%v versions=%d", err, len(got.Versions))
	}
	if !strings.Contains(got.Versions[0].Body, "Generate the repo's high-level codegraph") {
		t.Errorf("library task body did not round-trip: %q", got.Versions[0].Body)
	}

	// A project-scope task through this generic path is still rejected — only the
	// library copy is exempt; project tasks mint via createTask.
	if _, _, err := docStore.Upsert(ctx, document.UpsertInput{
		Key:  document.Key{Scope: document.ScopeProject, WorkspaceID: ws.ID, ProjectID: pub.ID, Name: "proj-task"},
		Type: document.TypeTask, Body: body, CreatedBy: "usr_dev_admin",
	}, now); err == nil {
		t.Error("a project-scope task via Store.Upsert must still be rejected (only scope:library is exempt)")
	}
}
