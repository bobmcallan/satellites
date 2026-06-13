//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/synth"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
	"github.com/chromedp/chromedp"
)

// TestWorkspaceObjective_Chromedp is the DOGFOOD evidence for sty_a0099c04: the
// server-side objective task (real Gemini) synthesizes a non-empty objective
// from a seeded corpus and stores it as the workspace 'objective' document
// (AC1/AC2), re-running bumps the version (AC3), the workspace page renders the
// objective body replacing the placeholder (AC2/AC5), and a disabled generator
// returns a noted not-generated result (AC4). Skips without GEMINI_API_KEY (the
// techdebt traverse runs it locally with the key).
func TestWorkspaceObjective_Chromedp(t *testing.T) {
	key := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	if key == "" {
		t.Skip("GEMINI_API_KEY not set — objective synthesis dogfood needs the live Gemini key")
	}
	env := testbootstrap.SetUpWithServer(t)

	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	docStore := document.New(env.DB)
	ledStore := ledger.New(env.DB)
	verb.SetAuthStore(env.Store)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	verb.SetDocumentStore(docStore)
	verb.SetLedgerStore(ledStore)
	verb.SetObjectiveService(synth.NewObjectiveService(synth.NewGeminiGenerator(key, ""), docStore))
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetDocumentStore(nil)
		verb.SetLedgerStore(nil)
		verb.SetObjectiveService(nil)
	})

	ctx := context.Background()
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	ws, err := wsStore.Create(ctx, "", "objective-demo", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	seed := func(name, body string) {
		if _, _, err := docStore.Upsert(ctx, document.UpsertInput{
			Key:  document.Key{Scope: document.ScopeWorkspace, WorkspaceID: ws.ID, Name: name},
			Type: document.TypeDocument, Body: body, CreatedBy: "usr_dev_admin",
		}, now); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	seed("client-brief", "# Client brief\n\nAcme Corp wants to migrate their legacy billing system to a modern cloud platform within two quarters, reducing invoice errors and manual reconciliation.")
	seed("meeting-notes", "# Kickoff notes\n\nStakeholders prioritised data integrity during migration and a phased cutover. Success = zero billing downtime and an audited reconciliation trail.")

	admin, err := env.Store.GetUserByID(ctx, "usr_dev_admin")
	if err != nil || admin == nil {
		t.Fatalf("get admin: %v", err)
	}
	ctxAdmin := auth.WithUser(ctx, admin)

	// AC1/AC2: generate the objective server-side (Gemini) → stored doc.
	genReq, _ := json.Marshal(verb.WorkspaceObjectiveGenerateRequest{WorkspaceID: ws.ID})
	raw, err := verb.Dispatch(ctxAdmin, "workspace_objective_generate", genReq)
	if err != nil {
		t.Fatalf("objective generate: %v", err)
	}
	var resp verb.WorkspaceObjectiveGenerateResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Generated || strings.TrimSpace(resp.Objective) == "" || resp.DocumentID == "" {
		t.Fatalf("expected a generated non-empty objective, got %+v", resp)
	}

	got, err := docStore.Get(ctx, document.Key{Scope: document.ScopeWorkspace, WorkspaceID: ws.ID, Name: "objective"}, document.GetOptions{})
	if err != nil {
		t.Fatalf("get objective doc: %v", err)
	}
	if strings.TrimSpace(got.Document.Name) != "objective" || got.Document.LatestVersion != 1 {
		t.Errorf("objective doc = %q v%d, want objective v1", got.Document.Name, got.Document.LatestVersion)
	}

	// AC3: re-running refreshes the same document (version bumps), not a duplicate.
	if _, err := verb.Dispatch(ctxAdmin, "workspace_objective_generate", genReq); err != nil {
		t.Fatalf("objective regenerate: %v", err)
	}
	got2, err := docStore.Get(ctx, document.Key{Scope: document.ScopeWorkspace, WorkspaceID: ws.ID, Name: "objective"}, document.GetOptions{})
	if err != nil {
		t.Fatalf("get objective doc v2: %v", err)
	}
	if got2.Document.LatestVersion < 2 {
		t.Errorf("re-run objective version = %d, want >= 2 (refresh, not duplicate)", got2.Document.LatestVersion)
	}

	// AC2/AC5: the workspace page renders the objective body, not the placeholder.
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), chromedpHeadlessOpts()...)
	t.Cleanup(cancelAlloc)
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	t.Cleanup(cancelBrowser)
	bctx, cancelRun := context.WithTimeout(browserCtx, 60*time.Second)
	t.Cleanup(cancelRun)

	var objText string
	var placeholderPresent bool
	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/login"),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-action="dev-login-admin"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
		chromedp.Navigate(env.ServerURL+"/workspaces/"+ws.ID),
		chromedp.WaitVisible(`[data-section="objective"] [data-field="objective-body"]`, chromedp.ByQuery),
		chromedp.Text(`[data-field="objective-body"]`, &objText, chromedp.ByQuery),
		chromedp.Evaluate(`!!document.querySelector('[data-field="objective-placeholder"]')`, &placeholderPresent),
	); err != nil {
		t.Fatalf("workspace objective page: %v", err)
	}
	if strings.TrimSpace(objText) == "" {
		t.Error("objective body rendered empty on the page")
	}
	if placeholderPresent {
		t.Error("objective placeholder still shown though an objective exists")
	}

	// AC4: a disabled generator reports a clear not-generated result.
	verb.SetObjectiveService(synth.NewObjectiveService(nil, docStore))
	raw, err = verb.Dispatch(ctxAdmin, "workspace_objective_generate", genReq)
	if err != nil {
		t.Fatalf("disabled generate errored: %v", err)
	}
	var disabled verb.WorkspaceObjectiveGenerateResponse
	if err := json.Unmarshal(raw, &disabled); err != nil {
		t.Fatalf("decode disabled: %v", err)
	}
	if disabled.Generated || disabled.Note == "" {
		t.Errorf("disabled generate = %+v, want generated:false + note", disabled)
	}
}
