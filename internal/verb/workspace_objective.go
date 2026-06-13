// Workspace objective synthesis verb (sty_a0099c04) — generate (or refresh) a
// workspace's objective from its corpus using the SERVER executor (Gemini), and
// store it as the workspace document `objective`. The manager triggers this from
// Claude web via the MCP tool, or it runs autonomously; the client-side
// `claude -p` executor is the CLI's alternative. Read/write gated by workspace
// membership.

package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/synth"
)

// objectiveService is wired by cmd/satellites-server at boot when a Gemini key
// is present; nil otherwise (generation reports "not generated", never crashes).
var objectiveService *synth.ObjectiveService

// SetObjectiveService wires the server-side objective generator.
func SetObjectiveService(s *synth.ObjectiveService) { objectiveService = s }

type WorkspaceObjectiveGenerateRequest struct {
	WorkspaceID string `json:"workspace_id"`
}

type WorkspaceObjectiveGenerateResponse struct {
	Generated  bool   `json:"generated"`
	Objective  string `json:"objective,omitempty"`
	DocumentID string `json:"document_id,omitempty"`
	Note       string `json:"note,omitempty"`
}

func init() {
	Register(&Verb{
		Name:        "workspace_objective_generate",
		Description: "Synthesize (or refresh) a workspace's objective from its document corpus and store it as the workspace document 'objective'. Requires a server-side generator (GEMINI_API_KEY).",
		Invoke:      invokeWorkspaceObjectiveGenerate,
	})
}

func invokeWorkspaceObjectiveGenerate(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if documentStore == nil {
		return nil, fmt.Errorf("workspace_objective_generate: document store not configured")
	}
	var req WorkspaceObjectiveGenerateRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("workspace_objective_generate: bad request: %w", err)
		}
	}
	wsID := strings.TrimSpace(req.WorkspaceID)
	if wsID == "" {
		return nil, fmt.Errorf("workspace_objective_generate: %w: workspace_id required", ErrBadRequest)
	}
	if authStore != nil && !isWorkspaceMember(ctx, wsID) {
		return nil, fmt.Errorf("workspace_objective_generate: %w: not a member of workspace %s", ErrForbidden, wsID)
	}

	// No generator wired (no GEMINI_API_KEY) → a clear not-generated result,
	// never a crash (AC4).
	if objectiveService == nil || !objectiveService.Enabled() {
		return json.Marshal(WorkspaceObjectiveGenerateResponse{
			Generated: false,
			Note:      "no server-side generator (GEMINI_API_KEY unset) — run `satellites workspace objective <id> --executor claude` to synthesize locally",
		})
	}

	text, err := objectiveService.GenerateText(ctx, wsID)
	if err != nil {
		// A missing corpus (or a generation hiccup) is a reportable
		// not-generated result, not a verb error.
		return json.Marshal(WorkspaceObjectiveGenerateResponse{Generated: false, Note: err.Error()})
	}

	doc, _, err := documentStore.Upsert(ctx, document.UpsertInput{
		Key:       document.Key{Scope: document.ScopeWorkspace, WorkspaceID: wsID, Name: synth.ObjectiveDocName},
		Type:      document.TypeDocument,
		Body:      text,
		CreatedBy: callerUserID(ctx),
	}, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("workspace_objective_generate: store objective: %w", err)
	}
	return json.Marshal(WorkspaceObjectiveGenerateResponse{Generated: true, Objective: text, DocumentID: doc.ID})
}
