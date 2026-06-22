//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestReviewBarrierExemptsBodyUnchanged pins sty_dc44e359 over the REAL verb path
// (no mocked dispatch): a behaviour-kind (`principles:`-tagged) document_upsert that
// changes ONLY tags — body byte-identical to the stored version — is exempt from the
// review attestation (the barrier gates CONTENT, not tags), so `context curate`
// toggling `principles:always` works again. A CHANGED body is still refused without
// an attestation, proving the barrier stays intact for content.
func TestReviewBarrierExemptsBodyUnchanged(t *testing.T) {
	env := testbootstrap.SetUp(t)
	docStore := document.New(env.DB)
	authStore := auth.New(env.DB)
	wsStore := workspace.New(env.DB)
	verb.SetDocumentStore(docStore)
	verb.SetAuthStore(authStore)
	verb.SetWorkspaceStore(wsStore)
	t.Cleanup(func() {
		verb.SetDocumentStore(nil)
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
	})
	testbootstrap.Reset(t, env)
	if err := authStore.DevSeed(context.Background()); err != nil {
		t.Fatalf("dev seed: %v", err)
	}
	admin, err := authStore.GetUserByEmail(context.Background(), auth.DevAdminEmail)
	if err != nil {
		t.Fatalf("admin: %v", err)
	}
	ctxAdmin := authWithUser(context.Background(), admin)

	ws, err := wsStore.Create(context.Background(), admin.ID, "curate-ws", time.Now())
	if err != nil {
		t.Fatalf("ws: %v", err)
	}
	if err := wsStore.AddMember(context.Background(), ws.ID, admin.ID, workspace.RoleAdmin, admin.ID, time.Now()); err != nil {
		t.Fatalf("member: %v", err)
	}

	const body = "the principle body, unchanged across a tag toggle"
	// Seed a behaviour-kind principle directly (bypassing the verb barrier).
	if _, _, err := docStore.Upsert(context.Background(), document.UpsertInput{
		Key:       document.Key{Scope: document.ScopeWorkspace, WorkspaceID: ws.ID, Name: "p-curate"},
		Type:      "document",
		Body:      body,
		CreatedBy: admin.ID,
	}, time.Now()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	mk := func(tags []string, b string) json.RawMessage {
		raw, _ := json.Marshal(map[string]any{
			"name": "p-curate", "scope": "workspace", "workspace_id": ws.ID,
			"type": "document", "tags": tags, "body": b,
		})
		return raw
	}

	// 1) Tag-only patch (body unchanged), NO review attestation → ALLOWED (exempt).
	if _, err := verb.Dispatch(ctxAdmin, "document_upsert",
		mk([]string{"principles:global", "principles:always"}, body)); err != nil {
		t.Fatalf("body-unchanged metadata patch must be exempt from the barrier, got: %v", err)
	}

	// 2) Body CHANGED, NO attestation → REFUSED (barrier intact for content).
	if _, err := verb.Dispatch(ctxAdmin, "document_upsert",
		mk([]string{"principles:global"}, body+" CHANGED")); !errors.Is(err, verb.ErrForbidden) {
		t.Fatalf("body-changed behaviour write must require an attestation, got: %v", err)
	}
}
