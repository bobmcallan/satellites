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

	"github.com/bobmcallan/satellites/internal/agent"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/frontmatter"
	"github.com/bobmcallan/satellites/internal/synth"
)

// agentModel is the server-side agent executor, wired at boot when a Gemini key
// is present (nil otherwise → agent-mode runs report a clear not-run result).
var agentModel agent.AgentModel

// SetAgentModel wires the server-side agent harness model.
func SetAgentModel(m agent.AgentModel) { agentModel = m }

// Executor modes for a task run.
const (
	executorSingleShot = "single_shot"
	executorAgent      = "agent"
)

// taskSkillRef points at the kind:task skill whose body is the task spec. The key
// shape depends on scope: a workspace skill keys on the target workspace; a
// library or project skill keys on the publisher/owner project_id.
type taskSkillRef struct {
	Scope     string `json:"scope"`
	Name      string `json:"name"`
	ProjectID string `json:"project_id,omitempty"`
}

// taskSchedule is the cadence for a trigger:"schedule" task. IntervalSeconds is
// the gap between runs; the scheduler worker computes next_run_at from it.
type taskSchedule struct {
	IntervalSeconds int `json:"interval_seconds"`
}

// taskConfig is the persisted body of an agent-task document.
type taskConfig struct {
	TaskSkill  taskSkillRef  `json:"task_skill"`
	OutputName string        `json:"output_name"`
	Trigger    string        `json:"trigger"`
	Executor   string        `json:"executor,omitempty"` // single_shot (default) | agent
	Schedule   *taskSchedule `json:"schedule,omitempty"` // required when trigger=schedule
}

// Task triggers. on_demand runs only on an explicit workspace_task_run;
// schedule runs autonomously on a cadence; on_document_change runs when the
// workspace corpus changes. The last two are driven by the scheduler worker.
const (
	triggerOnDemand    = "on_demand"
	triggerSchedule    = "schedule"
	triggerOnDocChange = "on_document_change"
)

// minScheduleIntervalSeconds floors a scheduled cadence — autonomous LLM runs
// cost compute, so a too-frequent schedule is refused at upsert. Integration
// tests drive the worker with an injected clock, so this floor does not slow
// them.
const minScheduleIntervalSeconds = 60

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
	Executor    string       `json:"executor,omitempty"` // overrides the task's executor
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
	executor := strings.TrimSpace(req.Executor)
	// A stored task name resolves its skill + output + executor from the config.
	if tn := strings.TrimSpace(req.TaskName); tn != "" {
		cfg, err := loadTaskConfig(ctx, wsID, tn)
		if err != nil {
			return nil, fmt.Errorf("workspace_task_run: %w", err)
		}
		skill = cfg.TaskSkill
		outName = cfg.OutputName
		if executor == "" {
			executor = cfg.Executor
		}
	}

	resp, err := runWorkspaceTask(ctx, wsID, skill, outName, executor, callerUserID(ctx))
	if err != nil {
		return nil, fmt.Errorf("workspace_task_run: %w", err)
	}
	return json.Marshal(resp)
}

// runWorkspaceTask is the executor-agnostic run core: resolve the task skill,
// generate via the selected executor, and store the result as the named
// workspace document. It carries NO auth — the verb gates membership before
// calling it, and the scheduler worker calls it with a system context. A
// missing generator/model or a generation hiccup is a reportable not-run
// result (Ran:false + Note), never an error; only malformed input is an error.
func runWorkspaceTask(ctx context.Context, wsID string, skill taskSkillRef, outName, executor, createdBy string) (WorkspaceTaskRunResponse, error) {
	outName = strings.TrimSpace(outName)
	if outName == "" {
		return WorkspaceTaskRunResponse{}, fmt.Errorf("%w: output_name required (or task_name)", ErrBadRequest)
	}
	executor = strings.TrimSpace(executor)
	if executor == "" {
		executor = executorSingleShot
	}
	if executor != executorSingleShot && executor != executorAgent {
		return WorkspaceTaskRunResponse{}, fmt.Errorf("%w: unsupported executor %q", ErrBadRequest, executor)
	}

	key, err := resolveTaskSkillKey(skill, wsID)
	if err != nil {
		return WorkspaceTaskRunResponse{}, err
	}
	got, err := documentStore.Get(ctx, key, document.GetOptions{})
	if err != nil || len(got.Versions) == 0 {
		return WorkspaceTaskRunResponse{}, fmt.Errorf("%w: task skill %s/%s not found", ErrNotFound, key.Scope, key.Name)
	}
	spec := taskSpecFromSkillBody(got.Versions[0].Body)
	if spec == "" {
		return WorkspaceTaskRunResponse{}, fmt.Errorf("%w: task skill %s/%s has an empty spec", ErrBadRequest, key.Scope, key.Name)
	}

	// Generate via the selected executor. A missing generator/model (no
	// GEMINI_API_KEY) or a generation hiccup is a reportable not-run result,
	// never a crash.
	var text string
	switch executor {
	case executorAgent:
		if agentModel == nil {
			return WorkspaceTaskRunResponse{Ran: false, Note: "no server-side agent model (GEMINI_API_KEY unset)"}, nil
		}
		// The agent envelope is configuration, not code: the skill declares its
		// tools (frontmatter), the operating prompt is a system document, and the
		// task message is composed from the skill's Spec/Verifier/Environment.
		fm, _, ferr := frontmatter.Parse([]byte(got.Versions[0].Body))
		if ferr != nil {
			return WorkspaceTaskRunResponse{}, fmt.Errorf("%w: parse task skill frontmatter: %v", ErrBadRequest, ferr)
		}
		tools, dispatch, terr := agentToolsFromSkill(wsID, fm.Tools)
		if terr != nil {
			return WorkspaceTaskRunResponse{}, terr
		}
		sys, perr := readAgentSystemPrompt(ctx)
		if perr != nil {
			return WorkspaceTaskRunResponse{Ran: false, Note: perr.Error()}, nil
		}
		out, rerr := agent.Run(ctx, agentModel, sys, composeTaskMessage(spec), tools, dispatch, agent.DefaultMaxSteps)
		if rerr != nil {
			return WorkspaceTaskRunResponse{Ran: false, Note: rerr.Error()}, nil
		}
		text = out
	default: // executorSingleShot
		if objectiveService == nil || !objectiveService.Enabled() {
			return WorkspaceTaskRunResponse{Ran: false, Note: "no server-side generator (GEMINI_API_KEY unset)"}, nil
		}
		out, gerr := objectiveService.GenerateOverCorpus(ctx, wsID, spec, outName)
		if gerr != nil {
			return WorkspaceTaskRunResponse{Ran: false, Note: gerr.Error()}, nil
		}
		text = out
	}
	if strings.TrimSpace(text) == "" {
		return WorkspaceTaskRunResponse{Ran: false, Note: "executor returned empty output"}, nil
	}

	doc, _, err := documentStore.Upsert(ctx, document.UpsertInput{
		Key:       document.Key{Scope: document.ScopeWorkspace, WorkspaceID: wsID, Name: outName},
		Type:      document.TypeDocument,
		Body:      text,
		CreatedBy: createdBy,
	}, time.Now().UTC())
	if err != nil {
		return WorkspaceTaskRunResponse{}, fmt.Errorf("store result: %w", err)
	}
	return WorkspaceTaskRunResponse{Ran: true, OutputName: outName, DocumentID: doc.ID, Result: text}, nil
}

type WorkspaceTaskUpsertRequest struct {
	WorkspaceID string        `json:"workspace_id"`
	Name        string        `json:"name"`
	TaskSkill   taskSkillRef  `json:"task_skill"`
	OutputName  string        `json:"output_name"`
	Trigger     string        `json:"trigger,omitempty"`
	Executor    string        `json:"executor,omitempty"`
	Schedule    *taskSchedule `json:"schedule,omitempty"`
}

// WorkspaceTaskView is the wire shape for a task in upsert/list responses.
type WorkspaceTaskView struct {
	Name       string        `json:"name"`
	TaskSkill  taskSkillRef  `json:"task_skill"`
	OutputName string        `json:"output_name"`
	Trigger    string        `json:"trigger"`
	Executor   string        `json:"executor"`
	Schedule   *taskSchedule `json:"schedule,omitempty"`
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
	var schedule *taskSchedule
	switch trigger {
	case triggerOnDemand, triggerOnDocChange:
		// no schedule — on_demand runs explicitly, on_document_change reacts
		// to the corpus (the scheduler worker polls for changes).
	case triggerSchedule:
		if req.Schedule == nil || req.Schedule.IntervalSeconds <= 0 {
			return nil, fmt.Errorf("workspace_task_upsert: %w: trigger %q requires schedule.interval_seconds > 0", ErrBadRequest, triggerSchedule)
		}
		if req.Schedule.IntervalSeconds < minScheduleIntervalSeconds {
			return nil, fmt.Errorf("workspace_task_upsert: %w: schedule.interval_seconds must be >= %d", ErrBadRequest, minScheduleIntervalSeconds)
		}
		schedule = &taskSchedule{IntervalSeconds: req.Schedule.IntervalSeconds}
	default:
		return nil, fmt.Errorf("workspace_task_upsert: %w: unsupported trigger %q (%q|%q|%q)", ErrBadRequest, trigger, triggerOnDemand, triggerSchedule, triggerOnDocChange)
	}
	executor := strings.TrimSpace(req.Executor)
	if executor == "" {
		executor = executorSingleShot
	}
	if executor != executorSingleShot && executor != executorAgent {
		return nil, fmt.Errorf("workspace_task_upsert: %w: unsupported executor %q (single_shot|agent)", ErrBadRequest, executor)
	}

	if authStore != nil && !canManageWorkspace(ctx, wsID) {
		return nil, fmt.Errorf("workspace_task_upsert: %w: not an admin of workspace %s", ErrForbidden, wsID)
	}

	cfg := taskConfig{TaskSkill: req.TaskSkill, OutputName: outName, Trigger: trigger, Executor: executor, Schedule: schedule}
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
	return json.Marshal(WorkspaceTaskView{Name: name, TaskSkill: cfg.TaskSkill, OutputName: cfg.OutputName, Trigger: cfg.Trigger, Executor: cfg.Executor, Schedule: cfg.Schedule})
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
		executor := cfg.Executor
		if executor == "" {
			executor = executorSingleShot
		}
		tasks = append(tasks, WorkspaceTaskView{
			Name:       strings.TrimPrefix(d.Name, synth.AgentTaskNamePrefix),
			TaskSkill:  cfg.TaskSkill,
			OutputName: cfg.OutputName,
			Trigger:    cfg.Trigger,
			Executor:   executor,
			Schedule:   cfg.Schedule,
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

// agentToolDef is one capability the harness CAN expose: its parameter schema,
// the fixed workspace-scoping forced into every call (a guardrail), and a
// description. This is mechanism — the catalogue of what exists. WHICH tools a
// task may use is configuration (the skill's frontmatter `tools`).
type agentToolDef struct {
	description string
	schema      json.RawMessage
	fixed       map[string]string
}

// agentToolCatalog is the harness's capability catalogue (mechanism), bound to
// the read-only verbs and scoped to one workspace. A kind:task skill selects from
// it by name; the binary never decides the policy.
func agentToolCatalog(wsID string) map[string]agentToolDef {
	return map[string]agentToolDef{
		"semantic_search": {
			description: "Search the workspace corpus by semantic similarity. Args: {query (string, required), limit (integer, optional)}.",
			schema:      json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer"}},"required":["query"]}`),
			fixed:       map[string]string{"workspace_id": wsID},
		},
		"document_get": {
			description: "Fetch a workspace document by name. Args: {name (string, required)}.",
			schema:      json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`),
			fixed:       map[string]string{"workspace_id": wsID, "scope": "workspace"},
		},
		"document_list": {
			description: "List workspace documents, optionally filtered by name_prefix. Args: {name_prefix (string, optional)}.",
			schema:      json.RawMessage(`{"type":"object","properties":{"name_prefix":{"type":"string"}}}`),
			fixed:       map[string]string{"workspace_id": wsID, "scope": "workspace", "type": "document"},
		},
	}
}

// agentToolsFromSkill builds the agent's tool set from the skill-declared
// allowlist (configuration) against the catalogue (mechanism). The dispatcher
// forces the workspace scoping into every call — so the agent reads only its own
// workspace, through the existing verb registry, and a tool the skill did not
// declare is unavailable.
func agentToolsFromSkill(wsID string, declared []string) ([]agent.Tool, agent.ToolDispatcher, error) {
	catalog := agentToolCatalog(wsID)
	allowed := map[string]agentToolDef{}
	tools := make([]agent.Tool, 0, len(declared))
	for _, name := range declared {
		def, ok := catalog[name]
		if !ok {
			return nil, nil, fmt.Errorf("%w: task skill declares unknown tool %q", ErrBadRequest, name)
		}
		allowed[name] = def
		tools = append(tools, agent.Tool{Name: name, Description: def.description, Parameters: def.schema})
	}
	dispatch := func(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
		def, ok := allowed[name]
		if !ok {
			return nil, fmt.Errorf("tool %q not permitted", name)
		}
		body, err := forceArgs(def.fixed, args)
		if err != nil {
			return nil, err
		}
		return Dispatch(ctx, name, body)
	}
	return tools, dispatch, nil
}

// forceArgs merges model-supplied args with the fixed args, fixed winning — so
// the agent can never override the workspace scoping (workspace_id/scope/type).
func forceArgs(fixed map[string]string, args json.RawMessage) (json.RawMessage, error) {
	merged := map[string]json.RawMessage{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &merged); err != nil {
			return nil, err
		}
	}
	for k, v := range fixed {
		b, _ := json.Marshal(v)
		merged[k] = b
	}
	return json.Marshal(merged)
}

// agentOperatingPromptName is the system-scope document holding the agent's
// operating framing — configuration, read at run time, not a Go literal.
const agentOperatingPromptName = "agent-operating-prompt"

// readAgentSystemPrompt loads the agent operating prompt from the substrate.
func readAgentSystemPrompt(ctx context.Context) (string, error) {
	got, err := documentStore.Get(ctx, document.Key{Scope: document.ScopeSystem, Name: agentOperatingPromptName}, document.GetOptions{})
	if err != nil || len(got.Versions) == 0 {
		return "", fmt.Errorf("agent operating prompt not configured (system/%s)", agentOperatingPromptName)
	}
	body := strings.TrimSpace(got.Versions[0].Body)
	if body == "" {
		return "", fmt.Errorf("agent operating prompt (system/%s) is empty", agentOperatingPromptName)
	}
	return body, nil
}

// composeTaskMessage builds the agent's task message from the kind:task skill's
// structure: the Spec is the deliverable, the Verifier a self-check, the
// Environment the operating constraints — interpreted, not dumped. A skill
// without those headings falls back to its whole body.
func composeTaskMessage(skillBody string) string {
	spec := extractMarkdownSection(skillBody, "Spec")
	verifier := extractMarkdownSection(skillBody, "Verifier")
	environment := extractMarkdownSection(skillBody, "Environment")
	if spec == "" && verifier == "" && environment == "" {
		return strings.TrimSpace(skillBody)
	}
	var b strings.Builder
	if spec != "" {
		b.WriteString(spec)
	} else {
		b.WriteString(strings.TrimSpace(skillBody))
	}
	if environment != "" {
		b.WriteString("\n\nOperating constraints:\n")
		b.WriteString(environment)
	}
	if verifier != "" {
		b.WriteString("\n\nBefore finalizing, self-check against:\n")
		b.WriteString(verifier)
	}
	return b.String()
}

// extractMarkdownSection returns the body under a `## <heading>` line, up to the
// next `## ` heading or end of document; empty when the heading is absent.
func extractMarkdownSection(body, heading string) string {
	lines := strings.Split(body, "\n")
	want := "## " + heading
	var out []string
	in := false
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if !in {
			if t == want {
				in = true
			}
			continue
		}
		if strings.HasPrefix(t, "## ") {
			break
		}
		out = append(out, ln)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
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
