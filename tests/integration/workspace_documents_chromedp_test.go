//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/blob"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ingest"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
	"github.com/chromedp/chromedp"
)

// TestWorkspaceDocuments_Chromedp is the DOGFOOD evidence for sty_3c2f02bf:
//   - ingestion at workspace scope through the real ingest helper extracts a
//     workspace-scoped document (AC1);
//   - a global admin may read/list any workspace's documents, a non-member is
//     refused (AC3);
//   - the workspace page lists the workspace's documents (AC2);
//   - the ingested document's extracted text is retrievable and it appears on
//     the page (AC4).
func TestWorkspaceDocuments_Chromedp(t *testing.T) {
	env := testbootstrap.SetUpWithServer(t)

	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	docStore := document.New(env.DB)
	ledStore := ledger.New(env.DB)
	blobStore := blob.New(env.DB)
	verb.SetAuthStore(env.Store)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	verb.SetDocumentStore(docStore)
	verb.SetLedgerStore(ledStore)
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetDocumentStore(nil)
		verb.SetLedgerStore(nil)
	})

	ctx := context.Background()
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	ws, err := wsStore.Create(ctx, "", "ws-docs-demo", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	// AC1: ingest a text blob at workspace scope through the real helper (the
	// identical path POST /workspaces/{id}/blobs and main.go use). PDF rides the
	// same extract.Text branch; markdown keeps the assertion deterministic.
	ref, err := ingest.StoreBlobAndExtract(ctx, blobStore, docStore, ingest.Upload{
		WorkspaceID: ws.ID,
		Filename:    "notes.md",
		ContentType: "text/markdown",
		CreatedBy:   "usr_dev_admin",
		Content:     []byte("# Notes\n\nThe quarterly objective is sustainable growth."),
	})
	if err != nil {
		t.Fatalf("ingest workspace blob: %v", err)
	}
	if !ref.Extracted || ref.DocumentID == "" {
		t.Fatalf("expected extraction to produce a document, got %+v", ref)
	}
	attachmentName := "attachment-" + ref.ID

	// Seed a second workspace document directly (store bypasses write authz),
	// tagged type:document so it shows in the type:document-filtered panel
	// (epic:phases-task-outputs).
	relDoc, _, err := docStore.Upsert(ctx, document.UpsertInput{
		Key:       document.Key{Scope: document.ScopeWorkspace, WorkspaceID: ws.ID, Name: "release-plan"},
		Type:      document.TypeDocument,
		Body:      "# Release Plan\n\nPhase one.",
		CreatedBy: "usr_dev_admin",
	}, now)
	if err != nil {
		t.Fatalf("seed workspace doc: %v", err)
	}
	if _, err := docStore.SetDocumentTags(ctx, relDoc.ID, []string{"type:document"}, now); err != nil {
		t.Fatalf("tag workspace doc: %v", err)
	}

	admin, err := env.Store.GetUserByID(ctx, "usr_dev_admin")
	if err != nil || admin == nil {
		t.Fatalf("get admin: %v", err)
	}
	user, err := env.Store.GetUserByID(ctx, "usr_dev_user")
	if err != nil || user == nil {
		t.Fatalf("get user: %v", err)
	}
	ctxAdmin := auth.WithUser(ctx, admin)
	ctxUser := auth.WithUser(ctx, user)

	// AC4: the extracted text is retrievable via document_get at workspace
	// scope. AC3: a global admin may read it though it owns no membership row.
	getReq, _ := json.Marshal(verb.DocumentGetRequest{Scope: "workspace", WorkspaceID: ws.ID, Name: attachmentName})
	gotRaw, err := verb.Dispatch(ctxAdmin, "document_get", getReq)
	if err != nil {
		t.Fatalf("admin document_get workspace attachment: %v", err)
	}
	var got verb.DocumentGetResponse
	if err := json.Unmarshal(gotRaw, &got); err != nil {
		t.Fatalf("decode document_get: %v", err)
	}
	if !strings.Contains(got.RawBody, "sustainable growth") {
		t.Errorf("extracted document body missing source text; got %q", got.RawBody)
	}

	// AC3: global admin may list any workspace's documents (bypass);
	// a non-member is refused.
	listReq, _ := json.Marshal(verb.DocumentListRequest{Scope: "workspace", WorkspaceID: ws.ID, Type: "document", Limit: 200})
	adminRaw, err := verb.Dispatch(ctxAdmin, "document_list", listReq)
	if err != nil {
		t.Fatalf("admin document_list workspace: %v", err)
	}
	var adminList verb.DocumentListResponse
	if err := json.Unmarshal(adminRaw, &adminList); err != nil {
		t.Fatalf("decode admin list: %v", err)
	}
	if len(adminList.Items) < 2 {
		t.Errorf("admin workspace doc list = %d items, want >= 2", len(adminList.Items))
	}
	if _, err := verb.Dispatch(ctxUser, "document_list", listReq); !errors.Is(err, verb.ErrForbidden) {
		t.Errorf("non-member document_list: err = %v, want ErrForbidden", err)
	}

	// AC2/AC4: the workspace page (as the dev admin) lists both documents.
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), chromedpHeadlessOpts()...)
	t.Cleanup(cancelAlloc)
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	t.Cleanup(cancelBrowser)
	bctx, cancelRun := context.WithTimeout(browserCtx, 60*time.Second)
	t.Cleanup(cancelRun)

	var docNames []string
	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/login"),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-action="dev-login-admin"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
		chromedp.Navigate(env.ServerURL+"/workspaces/"+ws.ID),
		chromedp.WaitVisible(`[data-section="documents"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-field="documents-table"]`, chromedp.ByQuery),
		chromedp.Evaluate(`Array.from(document.querySelectorAll('[data-field="document-row"]')).map(e => e.dataset.docName)`, &docNames),
	); err != nil {
		t.Fatalf("workspace documents page: %v", err)
	}
	nameSet := map[string]bool{}
	for _, n := range docNames {
		nameSet[n] = true
	}
	for _, want := range []string{attachmentName, "release-plan"} {
		if !nameSet[want] {
			t.Errorf("document %q missing from workspace page documents section; got %v", want, docNames)
		}
	}
}
