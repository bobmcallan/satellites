// workspace_task_run (epic:workspace-agents) — run a kind:task skill over a
// workspace's document corpus with the SERVER executor (Gemini) and store the
// result as a workspace document. This is the generalisation of
// workspace_objective_generate: the objective is one task; this runs any task
// spec. Read/write gated by workspace membership, like the objective verb.

package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
)

// taskSkillRef points at the kind:task skill whose body is the task spec. The key
// shape depends on scope: a workspace skill keys on the target workspace; a
// library or project skill keys on the publisher/owner project_id.
type taskSkillRef struct {
	Scope     string `json:"scope"`
	Name      string `json:"name"`
	ProjectID string `json:"project_id,omitempty"`
}

type WorkspaceTaskRunRequest struct {
	WorkspaceID string       `json:"workspace_id"`
	TaskSkill   taskSkillRef `json:"task_skill"`
	OutputName  string       `json:"output_name"`
}

type WorkspaceTaskRunResponse struct {
	Ran        bool   `json:"ran"`
	OutputName string `json:"output_name,omitempty"`
	DocumentID string `json:"document_id,omitempty"`
	Result     string `json:"result,omitempty"`
	Note       string `json:"note,omitempty"`
}

func init() {
	Register(&Verb{
		Name:        "workspace_task_run",
		Description: "Run a kind:task skill over a workspace's document corpus with the server executor (Gemini) and store the result as the named workspace document. Requires a server-side generator (GEMINI_API_KEY). Gated by workspace membership.",
		Invoke:      invokeWorkspaceTaskRun,
	})
}

func invokeWorkspaceTaskRun(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if documentStore == nil {
		return nil, fmt.Errorf("workspace_task_run: document store not configured")
	}
	var req WorkspaceTaskRunRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("workspace_task_run: bad request: %w", err)
		}
	}
	wsID := strings.TrimSpace(req.WorkspaceID)
	if wsID == "" {
		return nil, fmt.Errorf("workspace_task_run: %w: workspace_id required", ErrBadRequest)
	}
	outName := strings.TrimSpace(req.OutputName)
	if outName == "" {
		return nil, fmt.Errorf("workspace_task_run: %w: output_name required", ErrBadRequest)
	}
	skillName := strings.TrimSpace(req.TaskSkill.Name)
	if skillName == "" {
		return nil, fmt.Errorf("workspace_task_run: %w: task_skill.name required", ErrBadRequest)
	}
	scope := document.Scope(strings.TrimSpace(req.TaskSkill.Scope))
	if scope == "" {
		return nil, fmt.Errorf("workspace_task_run: %w: task_skill.scope required", ErrBadRequest)
	}

	if authStore != nil && !isWorkspaceMember(ctx, wsID) {
		return nil, fmt.Errorf("workspace_task_run: %w: not a member of workspace %s", ErrForbidden, wsID)
	}

	// Resolve the task skill body by scope.
	key := document.Key{Scope: scope, Name: skillName}
	switch scope {
	case document.ScopeWorkspace:
		key.WorkspaceID = wsID
	case document.ScopeLibrary, document.ScopeProject:
		key.ProjectID = strings.TrimSpace(req.TaskSkill.ProjectID)
		if key.ProjectID == "" {
			return nil, fmt.Errorf("workspace_task_run: %w: task_skill.project_id required for %s scope", ErrBadRequest, scope)
		}
	case document.ScopeSystem:
		// system skills key on name alone
	default:
		return nil, fmt.Errorf("workspace_task_run: %w: unsupported task_skill.scope %q", ErrBadRequest, scope)
	}
	got, err := documentStore.Get(ctx, key, document.GetOptions{})
	if err != nil || len(got.Versions) == 0 {
		return nil, fmt.Errorf("workspace_task_run: %w: task skill %s/%s not found", ErrNotFound, scope, skillName)
	}
	spec := taskSpecFromSkillBody(got.Versions[0].Body)
	if spec == "" {
		return nil, fmt.Errorf("workspace_task_run: %w: task skill %s/%s has an empty spec", ErrBadRequest, scope, skillName)
	}

	// No generator wired (no GEMINI_API_KEY) → a clear not-run result, never a crash.
	if objectiveService == nil || !objectiveService.Enabled() {
		return json.Marshal(WorkspaceTaskRunResponse{
			Ran:  false,
			Note: "no server-side generator (GEMINI_API_KEY unset)",
		})
	}

	// A missing corpus (or generation hiccup) is a reportable not-run result.
	text, err := objectiveService.GenerateOverCorpus(ctx, wsID, spec, outName)
	if err != nil {
		return json.Marshal(WorkspaceTaskRunResponse{Ran: false, Note: err.Error()})
	}

	doc, _, err := documentStore.Upsert(ctx, document.UpsertInput{
		Key:       document.Key{Scope: document.ScopeWorkspace, WorkspaceID: wsID, Name: outName},
		Type:      document.TypeDocument,
		Body:      text,
		CreatedBy: callerUserID(ctx),
	}, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("workspace_task_run: store result: %w", err)
	}
	return json.Marshal(WorkspaceTaskRunResponse{Ran: true, OutputName: outName, DocumentID: doc.ID, Result: text})
}

// taskSpecFromSkillBody extracts the instruction text from a kind:task skill body:
// it drops a leading YAML frontmatter block and any satellites-sync / library HTML
// stamp comment lines, leaving the markdown the run feeds the generator as the spec.
func taskSpecFromSkillBody(body string) string {
	s := strings.TrimLeft(body, "\n")
	if strings.HasPrefix(s, "---") {
		if idx := strings.Index(s, "\n---"); idx >= 0 {
			rest := s[idx+len("\n---"):]
			if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
				s = rest[nl+1:]
			} else {
				s = ""
			}
		}
	}
	var keep []string
	for _, ln := range strings.Split(s, "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "<!-- satellites-sync:begin") || strings.HasPrefix(t, "<!-- satellites-library:begin") {
			continue
		}
		keep = append(keep, ln)
	}
	return strings.TrimSpace(strings.Join(keep, "\n"))
}
