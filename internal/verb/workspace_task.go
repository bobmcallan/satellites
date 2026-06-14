// Workspace agent tasks (epic:workspace-agents). A workspace agent-task is a
// managed, named binding of a kind:task skill to an output document and a
// trigger, run over the workspace corpus by the SERVER executor (Gemini).
//
//   - workspace_task_upsert — define/curate a task (workspace owner/admin).
//   - workspace_task_list   — list a workspace's tasks (any member).
//   - workspace_task_run    — run a task, by stored name or inline skill ref.
//
// Tasks persist as workspace-scoped documents under the reserved name namespace
// synth.AgentTaskNamePrefix ("agent-task/"); the body is the JSON config. That
// namespace is excluded from the corpus (configs are not content).

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

// taskSkillRef points at the kind:task skill whose body is the task spec. The key
// shape depends on scope: a workspace skill keys on the target workspace; a
// library or project skill keys on the publisher/owner project_id.
type taskSkillRef struct {
	Scope     string `json:"scope"`
	Name      string `json:"name"`
	ProjectID string `json:"project_id,omitempty"`
}

// taskConfig is the persisted body of an agent-task document.
type taskConfig struct {
	TaskSkill  taskSkillRef `json:"task_skill"`
	OutputName string       `json:"output_name"`
	Trigger    string       `json:"trigger"`
}

// triggerOnDemand is the only trigger supported in this slice; schedule and
// document-change triggers arrive in later epic-order stories.
const triggerOnDemand = "on_demand"

func init() {
	Register(&Verb{
		Name:        "workspace_task_run",
		Description: "Run a workspace agent-task over the workspace corpus with the server executor (Gemini) and store the result as the named workspace document. Supply a stored task_name, or an inline task_skill + output_name. Requires a server-side generator (GEMINI_API_KEY). Gated by workspace membership.",
		Invoke:      invokeWorkspaceTaskRun,
	})
	Register(&Verb{
		Name:        "workspace_task_upsert",
		Description: "Create or update a workspace agent-task: a named binding of a kind:task skill to an output document and a trigger. Gated to the workspace owner/admin.",
		Invoke:      invokeWorkspaceTaskUpsert,
		MCPRole:     MCPRoleWorkspaceAdmin,
	})
	Register(&Verb{
		Name:        "workspace_task_list",
		Description: "List a workspace's agent-tasks (name, skill ref, output document, trigger). Gated by workspace membership.",
		Invoke:      invokeWorkspaceTaskList,
	})
}

// resolveTaskSkillKey builds the document key for a task skill ref, validating
// the scope and the project_id requirement for library/project scopes.
func resolveTaskSkillKey(ref taskSkillRef, wsID string) (document.Key, error) {
	name := strings.TrimSpace(ref.Name)
	if name == "" {
		return document.Key{}, fmt.Errorf("%w: task_skill.name required", ErrBadRequest)
	}
	scope := document.Scope(strings.TrimSpace(ref.Scope))
	if scope == "" {
		return document.Key{}, fmt.Errorf("%w: task_skill.scope required", ErrBadRequest)
	}
	key := document.Key{Scope: scope, Name: name}
	switch scope {
	case document.ScopeWorkspace:
		key.WorkspaceID = wsID
	case document.ScopeLibrary, document.ScopeProject:
		key.ProjectID = strings.TrimSpace(ref.ProjectID)
		if key.ProjectID == "" {
			return document.Key{}, fmt.Errorf("%w: task_skill.project_id required for %s scope", ErrBadRequest, scope)
		}
	case document.ScopeSystem:
		// system skills key on name alone
	default:
		return document.Key{}, fmt.Errorf("%w: unsupported task_skill.scope %q", ErrBadRequest, scope)
	}
	return key, nil
}

// taskDocName maps a task name to its reserved workspace-document name.
func taskDocName(name string) string { return synth.AgentTaskNamePrefix + name }

type WorkspaceTaskRunRequest struct {
	WorkspaceID string       `json:"workspace_id"`
	TaskName    string       `json:"task_name,omitempty"`
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
	if authStore != nil && !isWorkspaceMember(ctx, wsID) {
		return nil, fmt.Errorf("workspace_task_run: %w: not a member of workspace %s", ErrForbidden, wsID)
	}

	skill := req.TaskSkill
	outName := strings.TrimSpace(req.OutputName)
	// A stored task name resolves its skill + output from the config document.
	if tn := strings.TrimSpace(req.TaskName); tn != "" {
		cfg, err := loadTaskConfig(ctx, wsID, tn)
		if err != nil {
			return nil, fmt.Errorf("workspace_task_run: %w", err)
		}
		skill = cfg.TaskSkill
		outName = cfg.OutputName
	}
	if outName == "" {
		return nil, fmt.Errorf("workspace_task_run: %w: output_name required (or task_name)", ErrBadRequest)
	}

	key, err := resolveTaskSkillKey(skill, wsID)
	if err != nil {
		return nil, fmt.Errorf("workspace_task_run: %w", err)
	}
	got, err := documentStore.Get(ctx, key, document.GetOptions{})
	if err != nil || len(got.Versions) == 0 {
		return nil, fmt.Errorf("workspace_task_run: %w: task skill %s/%s not found", ErrNotFound, key.Scope, key.Name)
	}
	spec := taskSpecFromSkillBody(got.Versions[0].Body)
	if spec == "" {
		return nil, fmt.Errorf("workspace_task_run: %w: task skill %s/%s has an empty spec", ErrBadRequest, key.Scope, key.Name)
	}

	// No generator wired (no GEMINI_API_KEY) → a clear not-run result, never a crash.
	if objectiveService == nil || !objectiveService.Enabled() {
		return json.Marshal(WorkspaceTaskRunResponse{
			Ran:  false,
			Note: "no server-side generator (GEMINI_API_KEY unset)",
		})
	}

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

type WorkspaceTaskUpsertRequest struct {
	WorkspaceID string       `json:"workspace_id"`
	Name        string       `json:"name"`
	TaskSkill   taskSkillRef `json:"task_skill"`
	OutputName  string       `json:"output_name"`
	Trigger     string       `json:"trigger,omitempty"`
}

// WorkspaceTaskView is the wire shape for a task in upsert/list responses.
type WorkspaceTaskView struct {
	Name       string       `json:"name"`
	TaskSkill  taskSkillRef `json:"task_skill"`
	OutputName string       `json:"output_name"`
	Trigger    string       `json:"trigger"`
}

func invokeWorkspaceTaskUpsert(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if documentStore == nil {
		return nil, fmt.Errorf("workspace_task_upsert: document store not configured")
	}
	var req WorkspaceTaskUpsertRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("workspace_task_upsert: bad request: %w", err)
		}
	}
	wsID := strings.TrimSpace(req.WorkspaceID)
	if wsID == "" {
		return nil, fmt.Errorf("workspace_task_upsert: %w: workspace_id required", ErrBadRequest)
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("workspace_task_upsert: %w: name required", ErrBadRequest)
	}
	if strings.Contains(name, "/") {
		return nil, fmt.Errorf("workspace_task_upsert: %w: name must not contain '/'", ErrBadRequest)
	}
	outName := strings.TrimSpace(req.OutputName)
	if outName == "" {
		return nil, fmt.Errorf("workspace_task_upsert: %w: output_name required", ErrBadRequest)
	}
	if _, err := resolveTaskSkillKey(req.TaskSkill, wsID); err != nil {
		return nil, fmt.Errorf("workspace_task_upsert: %w", err)
	}
	trigger := strings.TrimSpace(req.Trigger)
	if trigger == "" {
		trigger = triggerOnDemand
	}
	if trigger != triggerOnDemand {
		return nil, fmt.Errorf("workspace_task_upsert: %w: unsupported trigger %q (only %q in this slice)", ErrBadRequest, trigger, triggerOnDemand)
	}

	if authStore != nil && !canManageWorkspace(ctx, wsID) {
		return nil, fmt.Errorf("workspace_task_upsert: %w: not an admin of workspace %s", ErrForbidden, wsID)
	}

	cfg := taskConfig{TaskSkill: req.TaskSkill, OutputName: outName, Trigger: trigger}
	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("workspace_task_upsert: marshal config: %w", err)
	}
	if _, _, err := documentStore.Upsert(ctx, document.UpsertInput{
		Key:       document.Key{Scope: document.ScopeWorkspace, WorkspaceID: wsID, Name: taskDocName(name)},
		Type:      document.TypeDocument,
		Body:      string(body),
		CreatedBy: callerUserID(ctx),
	}, time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("workspace_task_upsert: store task: %w", err)
	}
	return json.Marshal(WorkspaceTaskView{Name: name, TaskSkill: cfg.TaskSkill, OutputName: cfg.OutputName, Trigger: cfg.Trigger})
}

type WorkspaceTaskListRequest struct {
	WorkspaceID string `json:"workspace_id"`
}

type WorkspaceTaskListResponse struct {
	Tasks []WorkspaceTaskView `json:"tasks"`
}

func invokeWorkspaceTaskList(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if documentStore == nil {
		return nil, fmt.Errorf("workspace_task_list: document store not configured")
	}
	var req WorkspaceTaskListRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("workspace_task_list: bad request: %w", err)
		}
	}
	wsID := strings.TrimSpace(req.WorkspaceID)
	if wsID == "" {
		return nil, fmt.Errorf("workspace_task_list: %w: workspace_id required", ErrBadRequest)
	}
	if authStore != nil && !isWorkspaceMember(ctx, wsID) {
		return nil, fmt.Errorf("workspace_task_list: %w: not a member of workspace %s", ErrForbidden, wsID)
	}

	res, err := documentStore.List(ctx, document.ListFilter{
		Type:        document.TypeDocument,
		Scope:       document.ScopeWorkspace,
		WorkspaceID: wsID,
		NamePrefix:  synth.AgentTaskNamePrefix,
	}, document.ListOptions{Limit: 200})
	if err != nil {
		return nil, fmt.Errorf("workspace_task_list: %w", err)
	}
	tasks := []WorkspaceTaskView{}
	for _, d := range res.Items {
		got, err := documentStore.Get(ctx, document.Key{Scope: document.ScopeWorkspace, WorkspaceID: wsID, Name: d.Name}, document.GetOptions{})
		if err != nil || len(got.Versions) == 0 {
			continue
		}
		var cfg taskConfig
		if json.Unmarshal([]byte(got.Versions[0].Body), &cfg) != nil {
			continue // skip a malformed config rather than failing the whole list
		}
		tasks = append(tasks, WorkspaceTaskView{
			Name:       strings.TrimPrefix(d.Name, synth.AgentTaskNamePrefix),
			TaskSkill:  cfg.TaskSkill,
			OutputName: cfg.OutputName,
			Trigger:    cfg.Trigger,
		})
	}
	return json.Marshal(WorkspaceTaskListResponse{Tasks: tasks})
}

// loadTaskConfig reads and parses a stored agent-task config document.
func loadTaskConfig(ctx context.Context, wsID, name string) (taskConfig, error) {
	got, err := documentStore.Get(ctx, document.Key{Scope: document.ScopeWorkspace, WorkspaceID: wsID, Name: taskDocName(name)}, document.GetOptions{})
	if err != nil || len(got.Versions) == 0 {
		return taskConfig{}, fmt.Errorf("%w: task %q not found in workspace %s", ErrNotFound, name, wsID)
	}
	var cfg taskConfig
	if err := json.Unmarshal([]byte(got.Versions[0].Body), &cfg); err != nil {
		return taskConfig{}, fmt.Errorf("%w: task %q has a malformed config", ErrBadRequest, name)
	}
	return cfg, nil
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
