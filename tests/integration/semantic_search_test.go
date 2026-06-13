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
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/embed"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// keywordEmbedder is a deterministic stand-in for Gemini: it maps text to a
// keyword one-hot vector so ranking is exact and offline. Real embedding
// correctness is proven by epic-order:4's live-Gemini dogfood; this story tests
// the search SURFACE (verb → MCP/CLI), authz, and provenance.
type keywordEmbedder struct{}

var embedKeywords = []string{"growth", "kubernetes", "security"}

func (keywordEmbedder) Dimension() int { return len(embedKeywords) + 1 }

func (keywordEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		lower := strings.ToLower(t)
		v := make([]float32, len(embedKeywords)+1)
		for k, kw := range embedKeywords {
			if strings.Contains(lower, kw) {
				v[k] = 1
			}
		}
		v[len(embedKeywords)] = 0.1 // small bias so a keyword-free vector isn't all-zero
		out[i] = v
	}
	return out, nil
}

// TestSemanticSearch is the DOGFOOD evidence for sty_e8db6032: the semantic_search
// verb (the path the MCP tool + CLI both dispatch) ranks the expected document
// first with resolved provenance (AC1/AC4), refuses a non-member (AC3), and
// returns a noted empty result when embeddings are unavailable (AC2).
func TestSemanticSearch(t *testing.T) {
	env := testbootstrap.SetUpWithServer(t) // dev-seeds usr_dev_admin / usr_dev_user

	wsStore := workspace.New(env.DB)
	docStore := document.New(env.DB)
	ledStore := ledger.New(env.DB)
	chunkStore := embed.NewChunkStore(env.DB)
	svc := embed.NewService(keywordEmbedder{}, chunkStore, docStore, wsStore, 512, 64)
	verb.SetAuthStore(env.Store)
	verb.SetWorkspaceStore(wsStore)
	verb.SetDocumentStore(docStore)
	verb.SetLedgerStore(ledStore)
	verb.SetEmbedService(svc)
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetDocumentStore(nil)
		verb.SetLedgerStore(nil)
		verb.SetEmbedService(nil)
	})

	ctx := context.Background()
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	ws, err := wsStore.Create(ctx, "", "search-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	upsertEmbed := func(name, body string) document.Document {
		d, _, err := docStore.Upsert(ctx, document.UpsertInput{
			Key:  document.Key{Scope: document.ScopeWorkspace, WorkspaceID: ws.ID, Name: name},
			Type: document.TypeDocument, Body: body, CreatedBy: "usr_dev_admin",
		}, now)
		if err != nil {
			t.Fatalf("upsert %s: %v", name, err)
		}
		if err := svc.EmbedDocument(ctx, ws.ID, d.ID, d.LatestVersion, body); err != nil {
			t.Fatalf("embed %s: %v", name, err)
		}
		return d
	}
	growth := upsertEmbed("growth-strategy", "# Growth strategy\n\nrevenue growth plan and enterprise pipeline expansion")
	upsertEmbed("infra-runbook", "# Runbook\n\nkubernetes deployment and pod scaling in the cluster")

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

	// AC1/AC4: a growth query ranks the growth document first, name resolved.
	req, _ := json.Marshal(verb.SemanticSearchRequest{WorkspaceID: ws.ID, Query: "what is the growth plan?", Limit: 5})
	raw, err := verb.Dispatch(ctxAdmin, "semantic_search", req)
	if err != nil {
		t.Fatalf("semantic_search: %v", err)
	}
	var resp verb.SemanticSearchResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) == 0 {
		t.Fatalf("expected results, got none")
	}
	if resp.Results[0].DocumentID != growth.ID {
		t.Errorf("top result = %s (%q), want growth %s", resp.Results[0].DocumentID, resp.Results[0].DocumentName, growth.ID)
	}
	if resp.Results[0].DocumentName != "growth-strategy" {
		t.Errorf("document name = %q, want growth-strategy", resp.Results[0].DocumentName)
	}
	if resp.Results[0].Score <= 0 {
		t.Errorf("score = %v, want > 0", resp.Results[0].Score)
	}

	// AC3: a non-member is refused.
	if _, err := verb.Dispatch(ctxUser, "semantic_search", req); !errors.Is(err, verb.ErrForbidden) {
		t.Errorf("non-member semantic_search: err = %v, want ErrForbidden", err)
	}

	// AC2: with no embedder wired, a noted empty result (non-fatal).
	verb.SetEmbedService(embed.NewService(nil, chunkStore, docStore, wsStore, 512, 64))
	raw, err = verb.Dispatch(ctxAdmin, "semantic_search", req)
	if err != nil {
		t.Fatalf("disabled search errored: %v", err)
	}
	var disabled verb.SemanticSearchResponse
	if err := json.Unmarshal(raw, &disabled); err != nil {
		t.Fatalf("decode disabled: %v", err)
	}
	if len(disabled.Results) != 0 || disabled.Note == "" {
		t.Errorf("disabled search = %+v, want empty results + a note", disabled)
	}
}
