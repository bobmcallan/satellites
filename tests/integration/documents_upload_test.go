//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/frontmatter"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestDocumentsUploadEndToEnd exercises the file-driven upload path:
// document_upsert with tags → idempotent re-run → ride-along sidecar
// on a story read. Mirrors what `satellites documents upload` does,
// dispatching the verb directly instead of shelling the CLI.
func TestDocumentsUploadEndToEnd(t *testing.T) {
	env := testbootstrap.SetUp(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	docStore := document.New(env.DB)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	verb.SetDocumentStore(docStore)
	verb.SetLedgerStore(ledger.New(env.DB))
	t.Cleanup(func() {
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetDocumentStore(nil)
		verb.SetLedgerStore(nil)
	})

	ws, err := wsStore.Create(ctx, "", "upload-ws", now)
	if err != nil {
		t.Fatalf("ws: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{
		WorkspaceID: ws.ID,
		Name:        "upload-pj",
		GitURL:      "https://github.com/example/upload.git",
	}, now)
	if err != nil {
		t.Fatalf("pj: %v", err)
	}
	story, err := docStore.CreateStory(ctx, document.CreateStoryInput{
		ProjectID:   pj.ID,
		WorkspaceID: ws.ID,
		Title:       "demo",
	}, now)
	if err != nil {
		t.Fatalf("story: %v", err)
	}

	body := "# commit-push after each story\n\nrun /commit-push at the end of every story.\n"
	upsert := func(label string) []byte {
		t.Helper()
		req, err := json.Marshal(map[string]any{
			"type":         "document",
			"scope":        "project",
			"workspace_id": ws.ID,
			"project_id":   pj.ID,
			"name":         "commit-push-after-each-story",
			"body":         body,
			// principles:always marks it a curated must-read so it rides along
			// the read-verb sidecar; the scope tag alone is on-demand only
			// (sty_05794178).
			"tags": []string{"principles:project", "principles:always"},
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		raw, err := verb.Dispatch(ctx, "document_upsert", req)
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		return raw
	}

	first := upsert("first upload")
	var firstResp struct {
		Document struct {
			ID            string   `json:"id"`
			Tags          []string `json:"tags"`
			LatestVersion int      `json:"latest_version"`
		} `json:"document"`
		Version struct {
			Version int `json:"version"`
		} `json:"version"`
	}
	if err := json.Unmarshal(first, &firstResp); err != nil {
		t.Fatalf("unmarshal first: %v", err)
	}
	if firstResp.Document.LatestVersion != 1 {
		t.Fatalf("first upload latest_version = %d, want 1", firstResp.Document.LatestVersion)
	}
	if !sortedEqual(firstResp.Document.Tags, []string{"principles:project", "principles:always"}) {
		t.Fatalf("first upload tags = %v, want [principles:project principles:always]", firstResp.Document.Tags)
	}

	// Idempotent re-run: no new version row, no tag rewrite.
	second := upsert("second upload (idempotent)")
	var secondResp struct {
		Document struct {
			LatestVersion int `json:"latest_version"`
		} `json:"document"`
		Version struct {
			Version int `json:"version"`
		} `json:"version"`
	}
	if err := json.Unmarshal(second, &secondResp); err != nil {
		t.Fatalf("unmarshal second: %v", err)
	}
	if secondResp.Document.LatestVersion != 1 {
		t.Fatalf("idempotent re-upload latest_version = %d, want 1 (no new version)", secondResp.Document.LatestVersion)
	}
	if secondResp.Version.Version != 1 {
		t.Fatalf("idempotent re-upload version = %d, want 1", secondResp.Version.Version)
	}

	// Sidecar delivers the uploaded principle on a story read.
	raw, err := verb.Dispatch(ctx, "document_get", json.RawMessage(`{"id":"`+story.ID+`"}`))
	if err != nil {
		t.Fatalf("document_get: %v", err)
	}
	var got verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal story: %v", err)
	}
	found := false
	for _, p := range got.Principles {
		if p.ID == firstResp.Document.ID && p.Scope == verb.PrincipleScopeProject && p.Body == body {
			found = true
		}
	}
	if !found {
		t.Fatalf("uploaded principle missing from sidecar: %s", string(raw))
	}
}

// TestSystemSeedFrontmatterTags verifies the boot reconciler picks up
// frontmatter tags from an embedded artifact and applies them via
// SetDocumentTags. Mirrors what cmd/satellites-server/main.go does at
// boot, without spinning the full server.
func TestSystemSeedFrontmatterTags(t *testing.T) {
	env := testbootstrap.SetUp(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	docStore := document.New(env.DB)
	sysSeedStore := document.NewSystemSeedStore(env.DB)

	raw := []byte("---\ntags: [principles:global]\n---\n# Body\n\nrule body\n")
	fm, body, err := frontmatter.Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// First apply: row appears, tags applied.
	res, err := document.ReconcileSystemSeed(ctx, sysSeedStore, docStore, "principle-test", string(body), fm.Tags, "test:seed", now)
	if err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if !res.Created || !res.Changed {
		t.Fatalf("first reconcile should report created+changed, got %+v", res)
	}
	doc, err := docStore.Get(ctx, document.Key{Scope: document.ScopeSystem, Name: "principle-test"}, document.GetOptions{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !sortedEqual(doc.Document.Tags, []string{"principles:global"}) {
		t.Fatalf("tags after first apply = %v, want [principles:global]", doc.Document.Tags)
	}

	// Second apply with same body + tags: no-op.
	res2, err := document.ReconcileSystemSeed(ctx, sysSeedStore, docStore, "principle-test", string(body), fm.Tags, "test:seed", now.Add(time.Hour))
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if res2.Created || res2.Changed {
		t.Fatalf("idempotent reconcile should report unchanged, got %+v", res2)
	}

	// Third apply with tags changed: re-applies and updates tags.
	res3, err := document.ReconcileSystemSeed(ctx, sysSeedStore, docStore, "principle-test", string(body), []string{"principles:global", "area:test"}, "test:seed", now.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("third reconcile: %v", err)
	}
	if !res3.Changed {
		t.Fatalf("tag-change reconcile should report changed, got %+v", res3)
	}
	doc2, err := docStore.Get(ctx, document.Key{Scope: document.ScopeSystem, Name: "principle-test"}, document.GetOptions{})
	if err != nil {
		t.Fatalf("get after tag change: %v", err)
	}
	if !sortedEqual(doc2.Document.Tags, []string{"principles:global", "area:test"}) {
		t.Fatalf("tags after tag-change = %v, want [principles:global, area:test]", doc2.Document.Tags)
	}
}

func sortedEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	ac := append([]string(nil), a...)
	bc := append([]string(nil), b...)
	sort.Strings(ac)
	sort.Strings(bc)
	for i := range ac {
		if ac[i] != bc[i] {
			return false
		}
	}
	return true
}
