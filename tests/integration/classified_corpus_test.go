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
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestClassifiedCorpus is the DOGFOOD evidence for sty_7716a8e1 (classified
// report corpus). It proves the two gaps the epic closes:
//
//  1. Reconcile embeds PROJECT(repo)-scope documents, not only workspace-scope —
//     a repo-bound classified report becomes retrievable via semantic search.
//  2. semantic_search can be SCOPED TO A CLASSIFICATION via a tag filter
//     (classification:<value>), returning the matching doc and excluding others
//     across both scopes.
//
// Skips without GEMINI_API_KEY so CI without the key stays green.
func TestClassifiedCorpus(t *testing.T) {
	key := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	if key == "" {
		t.Skip("GEMINI_API_KEY not set — classified corpus dogfood needs the live Gemini key")
	}
	env := testbootstrap.SetUp(t)

	wsStore := workspace.New(env.DB)
	projStore := project.New(env.DB)
	docStore := document.New(env.DB)
	chunkStore := embed.NewChunkStore(env.DB)
	svc := embed.NewService(embed.NewGeminiEmbedder(key, "", 0), chunkStore, docStore, wsStore, 512, 64)

	ctx := context.Background()
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)

	ws, err := wsStore.Create(ctx, "", "classified-demo", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	proj, err := projStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "repo-a"}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	// A scan/agent ADDS a repo-bound (project-scope) report WITH a classification
	// tag, and a workspace-bound report under a different classification.
	secReport, _, err := docStore.Upsert(ctx, document.UpsertInput{
		Key:  document.Key{Scope: document.ScopeProject, WorkspaceID: ws.ID, ProjectID: proj.ID, Name: "secrets-scan-report"},
		Type: document.TypeDocument, CreatedBy: "usr_dev_admin",
		Body: "# Secrets scan\n\nFound a hard-coded AWS access key and a leaked Slack webhook token committed in the config directory; these credentials must be rotated and removed from history.",
	}, now)
	if err != nil {
		t.Fatalf("upsert security report: %v", err)
	}
	if _, err := docStore.SetDocumentTags(ctx, secReport.ID, []string{"classification:security"}, now); err != nil {
		t.Fatalf("tag security report: %v", err)
	}

	growth, _, err := docStore.Upsert(ctx, document.UpsertInput{
		Key:  document.Key{Scope: document.ScopeWorkspace, WorkspaceID: ws.ID, Name: "growth-strategy"},
		Type: document.TypeDocument, CreatedBy: "usr_dev_admin",
		Body: "# Growth strategy\n\nRevenue growth focuses on enterprise sales pipeline expansion and annual recurring revenue upsell.",
	}, now)
	if err != nil {
		t.Fatalf("upsert growth: %v", err)
	}
	if _, err := docStore.SetDocumentTags(ctx, growth.ID, []string{"classification:strategy"}, now); err != nil {
		t.Fatalf("tag growth: %v", err)
	}

	// GAP 1: reconcile must embed the project(repo)-scope report, not only the
	// workspace-scope one.
	if err := svc.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, ok, _ := chunkStore.DocVersion(ctx, secReport.ID); !ok {
		t.Fatal("reconcile must embed the project(repo)-scope classified report")
	}
	if _, ok, _ := chunkStore.DocVersion(ctx, growth.ID); !ok {
		t.Fatal("reconcile must embed the workspace-scope report")
	}

	// GAP 2a: an unfiltered search ranks the repo-bound security report first for a
	// secrets query — repo-bound corpus is retrievable.
	res, err := svc.Search(ctx, ws.ID, "which credentials leaked and must be rotated?", 5, nil)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res) == 0 || res[0].DocumentID != secReport.ID {
		t.Fatalf("unfiltered top result = %v, want repo-bound security report %s", topDoc(res), secReport.ID)
	}

	// GAP 2b: scoping the SAME query to classification:strategy must exclude the
	// security report entirely and only return strategy-classified chunks.
	scoped, err := svc.Search(ctx, ws.ID, "which credentials leaked and must be rotated?", 5, []string{"classification:strategy"})
	if err != nil {
		t.Fatalf("scoped search: %v", err)
	}
	for _, c := range scoped {
		if c.DocumentID != growth.ID {
			t.Fatalf("classification:strategy scope leaked doc %s (want only %s)", c.DocumentID, growth.ID)
		}
	}

	// And scoping to classification:security returns the security report.
	secScoped, err := svc.Search(ctx, ws.ID, "credentials", 5, []string{"classification:security"})
	if err != nil {
		t.Fatalf("security scoped search: %v", err)
	}
	if len(secScoped) == 0 || secScoped[0].DocumentID != secReport.ID {
		t.Fatalf("classification:security scope top = %v, want %s", topDoc(secScoped), secReport.ID)
	}
}
