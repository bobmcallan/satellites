// Semantic search verb (sty_e8db6032) — query a workspace's embedded corpus by
// meaning. The query is embedded with the same embedder as the corpus, ranked
// by cosine similarity in the embed service, and returned with document
// provenance. Exposed as an MCP tool and the `satellites semantic-search` CLI
// command; both dispatch this one verb. Read-gated by workspace membership.

package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bobmcallan/satellites/internal/embed"
)

// embedService is wired by cmd/satellites-server at boot when a Gemini key is
// present; nil otherwise (search degrades to an empty, non-fatal result).
var embedService *embed.Service

// SetEmbedService wires the embedding service into the verb package.
func SetEmbedService(s *embed.Service) { embedService = s }

type SemanticSearchRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Query       string `json:"query"`
	Limit       int    `json:"limit,omitempty"`
}

// SemanticSearchResult is one ranked chunk with its document provenance + score.
type SemanticSearchResult struct {
	DocumentID   string  `json:"document_id"`
	DocumentName string  `json:"document_name"`
	ChunkIndex   int     `json:"chunk_index"`
	Content      string  `json:"content"`
	Score        float64 `json:"score"`
}

// SemanticSearchResponse carries the ranked results; Note explains an empty
// result that is intentional (embeddings unavailable / empty query) rather than
// an error.
type SemanticSearchResponse struct {
	Results []SemanticSearchResult `json:"results"`
	Note    string                 `json:"note,omitempty"`
}

func init() {
	Register(&Verb{
		Name:        "semantic_search",
		Description: "Search a workspace's document corpus by semantic similarity. Returns ranked chunks with document provenance + score. Requires server-side embeddings (GEMINI_API_KEY).",
		Invoke:      invokeSemanticSearch,
	})
}

func invokeSemanticSearch(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var req SemanticSearchRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("semantic_search: %w: %v", ErrBadRequest, err)
		}
	}
	wsID := strings.TrimSpace(req.WorkspaceID)
	if wsID == "" {
		return nil, fmt.Errorf("semantic_search: %w: workspace_id required", ErrBadRequest)
	}
	// Read surface: a workspace member or a global admin.
	if authStore != nil && !isWorkspaceMember(ctx, wsID) {
		return nil, fmt.Errorf("semantic_search: %w: not a member of workspace %s", ErrForbidden, wsID)
	}

	// Non-fatal empties: no embedder wired, or an empty query — return a noted
	// empty result rather than an error (AC2).
	if embedService == nil || !embedService.Enabled() {
		return json.Marshal(SemanticSearchResponse{Results: []SemanticSearchResult{}, Note: "embeddings unavailable on the server (GEMINI_API_KEY unset)"})
	}
	if strings.TrimSpace(req.Query) == "" {
		return json.Marshal(SemanticSearchResponse{Results: []SemanticSearchResult{}, Note: "empty query"})
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	scored, err := embedService.Search(ctx, wsID, req.Query, limit)
	if err != nil {
		return nil, fmt.Errorf("semantic_search: %w", err)
	}

	results := make([]SemanticSearchResult, 0, len(scored))
	names := map[string]string{}
	for _, sc := range scored {
		name, seen := names[sc.DocumentID]
		if !seen {
			if documentStore != nil {
				if d, err := documentStore.GetByID(ctx, sc.DocumentID); err == nil {
					name = d.Name
				}
			}
			names[sc.DocumentID] = name
		}
		results = append(results, SemanticSearchResult{
			DocumentID:   sc.DocumentID,
			DocumentName: name,
			ChunkIndex:   sc.ChunkIndex,
			Content:      sc.Content,
			Score:        sc.Score,
		})
	}
	return json.Marshal(SemanticSearchResponse{Results: results})
}
