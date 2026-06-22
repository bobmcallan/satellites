//go:build integration

package integration_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/embed"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestWorkspaceEmbeddings is the DOGFOOD evidence for sty_7f4f7e11. It uses the
// live Gemini key (GEMINI_API_KEY) to embed real workspace documents and asserts
// the full lifecycle: chunk+embed+store (AC1), a known query ranks the expected
// document first (AC4/AC5), re-embedding on change advances the chunk version,
// delete removes chunks (AC2), and the reconcile worker embeds a new document
// and prunes a deleted one (AC1). Skips when the key is absent so non-local
// environments (CI without the key) stay green.
func TestWorkspaceEmbeddings(t *testing.T) {
	key := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	if key == "" {
		t.Skip("GEMINI_API_KEY not set — workspace embeddings dogfood needs the live Gemini key")
	}
	env := testbootstrap.SetUp(t) // Postgres only; this test drives the stores directly.

	wsStore := workspace.New(env.DB)
	docStore := document.New(env.DB)
	chunkStore := embed.NewChunkStore(env.DB)
	svc := embed.NewService(embed.NewGeminiEmbedder(key, "", 0), chunkStore, docStore, wsStore, 512, 64)

	ctx := context.Background()
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	ws, err := wsStore.Create(ctx, "", "embed-demo", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	upsert := func(name, body string) document.Document {
		d, _, err := docStore.Upsert(ctx, document.UpsertInput{
			Key:  document.Key{Scope: document.ScopeWorkspace, WorkspaceID: ws.ID, Name: name},
			Type: document.TypeDocument, Body: body, CreatedBy: "usr_dev_admin",
		}, now)
		if err != nil {
			t.Fatalf("upsert %s: %v", name, err)
		}
		return d
	}

	growthBody := "# Growth strategy\n\nOur revenue growth plan focuses on expanding the enterprise sales pipeline and increasing annual recurring revenue through upsell and renewals."
	infraBody := "# Kubernetes runbook\n\nTo roll a deployment, scale the pods, drain the node, and restart the ingress controller in the cluster."
	growth := upsert("growth-strategy", growthBody)
	infra := upsert("infra-runbook", infraBody)

	// AC1: embed both documents → chunks + vectors stored, version stamped.
	if err := svc.EmbedDocument(ctx, ws.ID, growth.ID, growth.LatestVersion, growthBody); err != nil {
		t.Fatalf("embed growth: %v", err)
	}
	if err := svc.EmbedDocument(ctx, ws.ID, infra.ID, infra.LatestVersion, infraBody); err != nil {
		t.Fatalf("embed infra: %v", err)
	}
	chunks, err := chunkStore.WorkspaceChunks(ctx, ws.ID, nil)
	if err != nil {
		t.Fatalf("list chunks: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("want >= 2 chunks, got %d", len(chunks))
	}
	if v, ok, _ := chunkStore.DocVersion(ctx, growth.ID); !ok || v != growth.LatestVersion {
		t.Errorf("growth doc_version = %d ok=%v, want %d", v, ok, growth.LatestVersion)
	}

	// AC4/AC5: a revenue query ranks the growth document first.
	res, err := svc.Search(ctx, ws.ID, "what is the revenue growth plan?", 3, nil)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res) == 0 || res[0].DocumentID != growth.ID {
		t.Fatalf("search top result document = %v, want growth %s", topDoc(res), growth.ID)
	}

	// Re-embed on change: a new version replaces the chunk set and advances the
	// stamped doc_version.
	newBody := growthBody + "\n\nWe will also expand into the APAC region next fiscal year."
	growth2 := upsert("growth-strategy", newBody)
	if growth2.LatestVersion <= growth.LatestVersion {
		t.Fatalf("expected version bump, got %d → %d", growth.LatestVersion, growth2.LatestVersion)
	}
	if err := svc.EmbedDocument(ctx, ws.ID, growth2.ID, growth2.LatestVersion, newBody); err != nil {
		t.Fatalf("re-embed growth: %v", err)
	}
	if v, ok, _ := chunkStore.DocVersion(ctx, growth.ID); !ok || v != growth2.LatestVersion {
		t.Errorf("re-embed doc_version = %d ok=%v, want %d", v, ok, growth2.LatestVersion)
	}

	// AC2: delete removes the document's chunks.
	if err := svc.RemoveDocument(ctx, growth.ID); err != nil {
		t.Fatalf("remove document: %v", err)
	}
	if _, ok, _ := chunkStore.DocVersion(ctx, growth.ID); ok {
		t.Error("growth chunks should be gone after RemoveDocument")
	}

	// AC1 worker path: Reconcile embeds a freshly-added document…
	rollups := upsert("rollups", "# Rollups\n\nQuarterly board updates summarize pipeline coverage and cash burn.")
	if err := svc.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile embed: %v", err)
	}
	if _, ok, _ := chunkStore.DocVersion(ctx, rollups.ID); !ok {
		t.Error("reconcile should have embedded the new document")
	}
	// …and prunes a deleted one.
	if _, _, err := docStore.Delete(ctx, document.Key{Scope: document.ScopeWorkspace, WorkspaceID: ws.ID, Name: "rollups"}, "usr_dev_admin", false, now); err != nil {
		t.Fatalf("delete rollups: %v", err)
	}
	if err := svc.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile prune: %v", err)
	}
	if _, ok, _ := chunkStore.DocVersion(ctx, rollups.ID); ok {
		t.Error("reconcile should have pruned the deleted document's chunks")
	}
}

func topDoc(res []embed.ScoredChunk) string {
	if len(res) == 0 {
		return "(none)"
	}
	return res[0].DocumentID
}
