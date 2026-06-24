//go:build integration

package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/embed"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// fakeEmbedder is a deterministic, key-free embed.Embedder: it maps text to a
// bag-of-keywords vector over a fixed vocabulary (component i = occurrences of
// vocab[i], lowercased). Cosine similarity then ranks a chunk by how much its
// vocabulary overlaps the query — enough to prove ranking + cross-project
// candidate spanning WITHOUT a live Gemini key (sty_95751c71).
type fakeEmbedder struct{ vocab []string }

func (f fakeEmbedder) Dimension() int { return len(f.vocab) }

func (f fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		lower := strings.ToLower(t)
		v := make([]float32, len(f.vocab))
		for j, w := range f.vocab {
			v[j] = float32(strings.Count(lower, w))
		}
		out[i] = v
	}
	return out, nil
}

// TestWorkspaceCrossProjectSearch proves sty_95751c71's invariant deterministically:
// a SINGLE workspace-scoped search surfaces documents from TWO different projects in
// the same workspace (the PM cross-resource read). It drives the real
// Reconcile→WorkspaceChunks path (Reconcile embeds project-scope docs across every
// project via ListProjectDocsInWorkspace, keyed by workspace_id; Search ranks the
// whole workspace candidate set) with a fake embedder, so it needs no GEMINI_API_KEY —
// the key-free counterpart to TestClassifiedCorpus (which needs the live key and
// covers only one project). This is the deterministic coverage that the pprod
// verification (AC3, embeddings-gated) cannot provide from the repo.
func TestWorkspaceCrossProjectSearch(t *testing.T) {
	env := testbootstrap.SetUp(t) // Postgres only; this test drives the stores directly.

	wsStore := workspace.New(env.DB)
	projStore := project.New(env.DB)
	docStore := document.New(env.DB)
	chunkStore := embed.NewChunkStore(env.DB)
	emb := fakeEmbedder{vocab: []string{"codegraph", "discovery", "alpha", "beta"}}
	svc := embed.NewService(emb, chunkStore, docStore, wsStore, 512, 64)

	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)

	ws, err := wsStore.Create(ctx, "", "cross-project-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	// TWO sibling projects in ONE workspace — the proj_9ff86e59 / proj_43f5da1d shape.
	projA, err := projStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "meridian"}, now)
	if err != nil {
		t.Fatalf("create project A: %v", err)
	}
	projB, err := projStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "discovery"}, now)
	if err != nil {
		t.Fatalf("create project B: %v", err)
	}

	// A project-scope document in EACH project — distinct vocabulary so ranking is
	// observable (codegraph doc in A, discovery doc in B).
	upsertProjectDoc := func(projID, name, body string) document.Document {
		d, _, err := docStore.Upsert(ctx, document.UpsertInput{
			Key:  document.Key{Scope: document.ScopeProject, WorkspaceID: ws.ID, ProjectID: projID, Name: name},
			Type: document.TypeDocument, Body: body, CreatedBy: "usr_dev_admin",
		}, now)
		if err != nil {
			t.Fatalf("upsert %s: %v", name, err)
		}
		return d
	}
	cgDoc := upsertProjectDoc(projA.ID, "meridian-codegraph",
		"# Codegraph\n\nThe codegraph codegraph package import graph for the alpha service.")
	discDoc := upsertProjectDoc(projB.ID, "discovery-corpus",
		"# Discovery\n\nThe discovery discovery research corpus for the beta initiative.")

	// Reconcile is the production path: it must embed project-scope docs from BOTH
	// projects (ListProjectDocsInWorkspace filters by workspace_id only, no project
	// filter), keyed by the same workspace_id.
	if err := svc.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, ok, _ := chunkStore.DocVersion(ctx, cgDoc.ID); !ok {
		t.Fatalf("reconcile must embed project A's codegraph doc %s", cgDoc.ID)
	}
	if _, ok, _ := chunkStore.DocVersion(ctx, discDoc.ID); !ok {
		t.Fatalf("reconcile must embed project B's discovery doc %s", discDoc.ID)
	}

	// AC1: a SINGLE workspace-scoped search returns BOTH projects' documents in one
	// result set — the cross-project read.
	res, err := svc.Search(ctx, ws.ID, "codegraph discovery", 10, nil)
	if err != nil {
		t.Fatalf("cross-project search: %v", err)
	}
	got := map[string]bool{}
	for _, c := range res {
		got[c.DocumentID] = true
	}
	if !got[cgDoc.ID] || !got[discDoc.ID] {
		t.Fatalf("workspace search must return BOTH projects' docs in one result set; got %v (want %s from project A AND %s from project B)",
			docIDs(res), cgDoc.ID, discDoc.ID)
	}

	// Ranking is real (not just k returning everything): a codegraph-specific query
	// ranks project A's doc first, while project B's doc remains in the candidate set
	// (proving the candidate set spans both projects, not a single project).
	cgRes, err := svc.Search(ctx, ws.ID, "codegraph", 10, nil)
	if err != nil {
		t.Fatalf("codegraph search: %v", err)
	}
	if len(cgRes) == 0 || cgRes[0].DocumentID != cgDoc.ID {
		t.Fatalf("codegraph query top result = %v, want project A's codegraph doc %s", topDoc(cgRes), cgDoc.ID)
	}
	spans := false
	for _, c := range cgRes {
		if c.DocumentID == discDoc.ID {
			spans = true
			break
		}
	}
	if !spans {
		t.Errorf("candidate set must span project B too — project B's doc %s absent from the workspace search", discDoc.ID)
	}
}

// docIDs lists the distinct document ids in a result set (failure diagnostics).
func docIDs(res []embed.ScoredChunk) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range res {
		if !seen[c.DocumentID] {
			seen[c.DocumentID] = true
			out = append(out, c.DocumentID)
		}
	}
	return out
}
