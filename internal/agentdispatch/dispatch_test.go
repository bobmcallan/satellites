package agentdispatch

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/story"
	"github.com/bobmcallan/satellites/internal/task"
)

// dispatchFixture wires the in-memory stores, a tmpdir git repo, a
// stub claude binary, and a single agent + task + story so each test
// case can exercise Dispatch end-to-end without touching real Gemini
// or the operator's ~/.claude/.
type dispatchFixture struct {
	t           *testing.T
	repoPath    string
	stubBinDir  string
	claudePath  string
	docs        *document.MemoryStore
	tasks       *task.MemoryStore
	ledger      *ledger.MemoryStore
	stories     *story.MemoryStore
	projects    *project.MemoryStore
	agentID     string
	taskID      string
	storyID     string
	projectID   string
	workspaceID string
	now         time.Time
}

func newDispatchFixture(t *testing.T) *dispatchFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("agentdispatch tests rely on POSIX shell stub")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}

	tmp := t.TempDir()
	repoPath := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	mustGit(t, repoPath, "init", "--quiet", "--initial-branch=main")
	mustGit(t, repoPath, "config", "user.email", "test@example.com")
	mustGit(t, repoPath, "config", "user.name", "Test")
	mustGit(t, repoPath, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	mustGit(t, repoPath, "add", "README.md")
	mustGit(t, repoPath, "commit", "-q", "-m", "seed")

	stubBin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(stubBin, 0o755); err != nil {
		t.Fatalf("mkdir stub bin: %v", err)
	}
	stubPath := filepath.Join(stubBin, "claude")
	stub := `#!/bin/bash
# Stub claude binary for agentdispatch tests. Records its own args
# (one per line) to $DISPATCH_ARGS_FILE when set, then prints a known
# JSON envelope so parseClaudeEnvelope has something to chew on.
if [ -n "$DISPATCH_ARGS_FILE" ]; then
  for a in "$@"; do printf '%s\n' "$a" >> "$DISPATCH_ARGS_FILE"; done
fi
cat <<EOF
{"result":"stub-ok","session_id":"test-session-id","total_cost_usd":0.0123,"usage":{"input_tokens":100,"output_tokens":50}}
EOF
exit 0
`
	if err := os.WriteFile(stubPath, []byte(stub), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	docs := document.NewMemoryStore()
	tasks := task.NewMemoryStore()
	led := ledger.NewMemoryStore()
	stories := story.NewMemoryStore(led)
	projects := project.NewMemoryStore()

	wsID := "wksp_test"
	pj, err := projects.Create(context.Background(), "system", wsID, "test-project", now)
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	pid := pj.ID

	createdStory, err := stories.Create(context.Background(), story.Story{
		WorkspaceID:        wsID,
		ProjectID:          pid,
		Title:              "test story for dispatch",
		Description:        "fixture story body — proves story_context lands in the prompt",
		AcceptanceCriteria: "fixture AC — proves AC lands in the prompt",
		Status:             "in_progress",
		Priority:           "medium",
		Category:           "infrastructure",
		Tags:               []string{"test", "dispatch"},
		CreatedBy:          "system",
	}, now)
	if err != nil {
		t.Fatalf("seed story: %v", err)
	}
	storyID := createdStory.ID

	// Seed agent doc with delivers contract:develop and a known
	// permission_patterns list.
	settings := document.AgentSettings{
		PermissionPatterns: []string{"Read:**", "Edit:**", "Bash:go_test"},
		Delivers:           []string{"contract:develop"},
		Reviews:            []string{"contract:plan"},
	}
	settingsJSON, _ := document.MarshalAgentSettings(settings)
	agentDoc, err := docs.Create(context.Background(), document.Document{
		WorkspaceID: wsID,
		Type:        document.TypeAgent,
		Name:        "test_developer",
		Body:        "# Test Developer Agent\n\nfixture body — proves agent profile lands in the prompt.",
		Structured:  settingsJSON,
		Scope:       document.ScopeSystem,
		Status:      document.StatusActive,
		Tags:        []string{"test", "agent"},
	}, now)
	if err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	// Seed an active principle so the prompt's principles section has
	// something to assemble.
	if _, err := docs.Create(context.Background(), document.Document{
		WorkspaceID: wsID,
		Type:        document.TypePrinciple,
		Name:        "pr_test_principle",
		Body:        "Test principle body — proves principles land in the prompt.",
		Scope:       document.ScopeSystem,
		Status:      document.StatusActive,
	}, now); err != nil {
		t.Fatalf("seed principle: %v", err)
	}

	// Seed a contract doc so the prompt's contract section resolves.
	if _, err := docs.Create(context.Background(), document.Document{
		WorkspaceID: wsID,
		Type:        document.TypeContract,
		Name:        "develop",
		Body:        "Test develop contract body — proves contract lands in the prompt.",
		Scope:       document.ScopeSystem,
		Status:      document.StatusActive,
	}, now); err != nil {
		t.Fatalf("seed contract: %v", err)
	}

	// Seed an agent_process artifact so the prompt's agent_process
	// section resolves.
	if _, err := docs.Create(context.Background(), document.Document{
		WorkspaceID: wsID,
		Type:        document.TypeArtifact,
		Name:        "default_agent_process",
		Body:        "AGENT_PROCESS_FIXTURE_BODY — proves agent_process artifact lands in the prompt.",
		Scope:       document.ScopeSystem,
		Status:      document.StatusActive,
		Tags:        []string{"kind:agent-process"},
	}, now); err != nil {
		t.Fatalf("seed artifact: %v", err)
	}

	// Seed the work task. Status=published so subscriber-visible.
	tk, err := tasks.Enqueue(context.Background(), task.Task{
		WorkspaceID: wsID,
		ProjectID:   pid,
		StoryID:     storyID,
		Kind:        task.KindWork,
		Action:      "contract:develop",
		AgentID:     agentDoc.ID,
		Description: "test work task for dispatch fixture",
		Origin:      task.OriginStoryStage,
		Status:      task.StatusPublished,
	}, now)
	if err != nil {
		t.Fatalf("enqueue task: %v", err)
	}

	return &dispatchFixture{
		t:           t,
		repoPath:    repoPath,
		stubBinDir:  stubBin,
		claudePath:  stubPath,
		docs:        docs,
		tasks:       tasks,
		ledger:      led,
		stories:     stories,
		projects:    projects,
		agentID:     agentDoc.ID,
		taskID:      tk.ID,
		storyID:     storyID,
		projectID:   pid,
		workspaceID: wsID,
		now:         now,
	}
}

func (f *dispatchFixture) deps() Deps {
	return Deps{
		Tasks:    f.tasks,
		Docs:     f.docs,
		Ledger:   f.ledger,
		Stories:  f.stories,
		Projects: f.projects,
		Now:      func() time.Time { return f.now },
	}
}

func (f *dispatchFixture) cfg() Config {
	return Config{
		Mode:                      ModeBash,
		ClaudePath:                f.claudePath,
		TimeoutSeconds:            60,
		PreserveWorktreeOnFailure: false,
		RepoPath:                  f.repoPath,
		SubstrateMCPURL:           "http://substrate.test/mcp",
	}
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, string(out))
	}
	return string(out)
}

// TestDispatch_HappyPath_EndToEnd runs Dispatch with a stub claude
// binary. Asserts: subprocess invoked with the expected flags + args;
// settings.json and mcp.json land in the worktree with the right
// content shape; ledger row written; result fields populated;
// worktree torn down on success.
func TestDispatch_HappyPath_EndToEnd(t *testing.T) {
	f := newDispatchFixture(t)

	argsFile := filepath.Join(t.TempDir(), "claude-args.txt")
	t.Setenv("DISPATCH_ARGS_FILE", argsFile)

	res, err := Dispatch(context.Background(), f.cfg(), f.deps(), f.taskID, f.agentID)
	if err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got error=%q", res.Error)
	}
	if res.Branch == "" {
		t.Fatal("expected branch name in result")
	}
	if !strings.HasPrefix(res.Branch, "agent-"+f.taskID+"-from-") {
		t.Errorf("branch name shape unexpected: %q", res.Branch)
	}
	if res.EvidenceLedgerID == "" {
		t.Error("expected EvidenceLedgerID populated")
	}

	// Worktree torn down on success.
	if _, err := os.Stat(res.WorktreeDir); !os.IsNotExist(err) {
		t.Errorf("expected worktree torn down on success, stat err=%v", err)
	}

	// Branch ref preserved (story explicitly says preserve branch ref).
	headOut := mustGit(t, f.repoPath, "rev-parse", res.Branch)
	if strings.TrimSpace(headOut) == "" {
		t.Error("expected branch ref preserved after success teardown")
	}

	// Subprocess args captured. Must include --output-format json,
	// --strict-mcp-config, --mcp-config <path>, --allowedTools <patterns>.
	argsBytes, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args file: %v", err)
	}
	args := string(argsBytes)
	for _, want := range []string{
		"-p",
		"--output-format",
		"json",
		"--mcp-config",
		"--strict-mcp-config",
		"--allowedTools",
		"Read:**",
		"Edit:**",
		"Bash:go_test",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("expected stub claude args to contain %q, got:\n%s", want, args)
		}
	}

	// Ledger row written with the right tags.
	rows, err := f.ledger.List(context.Background(), f.projectID, ledger.ListOptions{
		StoryID: f.storyID,
		Limit:   100,
	}, nil)
	if err != nil {
		t.Fatalf("ledger list: %v", err)
	}
	var found *ledger.LedgerEntry
	for i := range rows {
		for _, tg := range rows[i].Tags {
			if tg == "kind:dispatch-result" {
				found = &rows[i]
				break
			}
		}
		if found != nil {
			break
		}
	}
	if found == nil {
		t.Fatal("expected kind:dispatch-result ledger row, none found")
	}
	if found.ID != res.EvidenceLedgerID {
		t.Errorf("EvidenceLedgerID mismatch: result=%q ledger=%q", res.EvidenceLedgerID, found.ID)
	}
	var structured map[string]any
	if err := json.Unmarshal(found.Structured, &structured); err != nil {
		t.Fatalf("decode structured payload: %v", err)
	}
	if structured["task_id"] != f.taskID {
		t.Errorf("structured.task_id mismatch: got %v", structured["task_id"])
	}
	if structured["agent_role"] != "test_developer" {
		t.Errorf("structured.agent_role mismatch: got %v", structured["agent_role"])
	}
	if structured["session_id"] != "test-session-id" {
		t.Errorf("structured.session_id mismatch: got %v", structured["session_id"])
	}
}

// TestDispatch_PromptAssembly_AllSixSourcesPresent calls composePrompt
// directly and asserts the six anchor headings (per
// pr_substrate_provides_context) are present + the seeded fixture
// content lands under each one. Proves the context-bundle invariant
// without depending on subprocess behaviour.
func TestDispatch_PromptAssembly_AllSixSourcesPresent(t *testing.T) {
	f := newDispatchFixture(t)
	tk, err := f.tasks.GetByID(context.Background(), f.taskID, nil)
	if err != nil {
		t.Fatalf("load task: %v", err)
	}
	agent, err := f.docs.GetByID(context.Background(), f.agentID, nil)
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}
	prompt := composePrompt(context.Background(), f.deps(), tk, agent)

	for _, anchor := range []string{
		AnchorAgentProcess,
		AnchorAgentProfile,
		AnchorPrinciples,
		AnchorStoryContext,
		AnchorContract,
		AnchorTaskChain,
	} {
		if !strings.Contains(prompt, anchor) {
			t.Errorf("prompt missing anchor %q", anchor)
		}
	}
	for _, fixtureToken := range []string{
		"AGENT_PROCESS_FIXTURE_BODY", // agent_process artifact
		"# Test Developer Agent",     // agent doc body
		"pr_test_principle",          // principles
		"Test principle body",        // principles
		"fixture story body",         // story_context (Description)
		"fixture AC",                 // story_context (AC)
		"Test develop contract body", // contract
		f.taskID,                     // task chain row
		"contract:develop",           // task chain action
	} {
		if !strings.Contains(prompt, fixtureToken) {
			t.Errorf("prompt missing fixture token %q", fixtureToken)
		}
	}
}

// TestDispatch_PermissionEnvelope_FilesAndFlag verifies the dual
// enforcement: the --allowedTools subprocess flag carries the
// permission patterns AND .claude/settings.json + .claude/mcp.json
// land with the right content shape. Uses a stub claude that exits
// non-zero so the worktree is preserved (PreserveWorktreeOnFailure)
// for inspection.
func TestDispatch_PermissionEnvelope_FilesAndFlag(t *testing.T) {
	f := newDispatchFixture(t)

	// Replace the stub with one that fails — preserves worktree so we
	// can inspect the on-disk files.
	failStub := `#!/bin/bash
echo "{}"
exit 7
`
	if err := os.WriteFile(f.claudePath, []byte(failStub), 0o755); err != nil {
		t.Fatalf("rewrite stub: %v", err)
	}

	cfg := f.cfg()
	cfg.PreserveWorktreeOnFailure = true

	res, err := Dispatch(context.Background(), cfg, f.deps(), f.taskID, f.agentID)
	if err != nil {
		t.Fatalf("Dispatch error: %v", err)
	}
	if res.Success {
		t.Fatal("expected failure (stub exit 7)")
	}
	if _, err := os.Stat(res.WorktreeDir); err != nil {
		t.Fatalf("worktree should be preserved on failure, stat err=%v", err)
	}
	defer func() {
		// Clean up the preserved worktree so the test repo doesn't leak.
		_ = exec.Command("git", "-C", f.repoPath, "worktree", "remove", "--force", res.WorktreeDir).Run()
	}()

	settingsBytes, err := os.ReadFile(filepath.Join(res.WorktreeDir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(settingsBytes, &settings); err != nil {
		t.Fatalf("decode settings.json: %v", err)
	}
	perms, ok := settings["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("settings.permissions wrong shape: %v", settings["permissions"])
	}
	allow, ok := perms["allow"].([]any)
	if !ok {
		t.Fatalf("settings.permissions.allow wrong shape: %v", perms["allow"])
	}
	want := map[string]bool{"Read:**": false, "Edit:**": false, "Bash:go_test": false}
	for _, a := range allow {
		if s, ok := a.(string); ok {
			if _, present := want[s]; present {
				want[s] = true
			}
		}
	}
	for k, found := range want {
		if !found {
			t.Errorf("settings.permissions.allow missing %q", k)
		}
	}
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("settings.hooks wrong shape: %v", settings["hooks"])
	}
	if _, ok := hooks["PreToolUse"]; !ok {
		t.Error("settings.hooks.PreToolUse missing")
	}
	if _, ok := hooks["Stop"]; !ok {
		t.Error("settings.hooks.Stop missing")
	}

	mcpBytes, err := os.ReadFile(filepath.Join(res.WorktreeDir, ".claude", "mcp.json"))
	if err != nil {
		t.Fatalf("read mcp.json: %v", err)
	}
	var mcp map[string]any
	if err := json.Unmarshal(mcpBytes, &mcp); err != nil {
		t.Fatalf("decode mcp.json: %v", err)
	}
	servers, ok := mcp["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers wrong shape: %v", mcp["mcpServers"])
	}
	sat, ok := servers["satellites"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers.satellites wrong shape: %v", servers["satellites"])
	}
	headers, ok := sat["headers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers.satellites.headers wrong shape: %v", sat["headers"])
	}
	if h, ok := headers["X-Satellites-Agent"].(string); !ok || h != "test_developer:"+f.taskID {
		t.Errorf("X-Satellites-Agent header mismatch: %v", headers["X-Satellites-Agent"])
	}
	if url, ok := sat["url"].(string); !ok || url != "http://substrate.test/mcp" {
		t.Errorf("mcpServers.satellites.url mismatch: %v", sat["url"])
	}
}

// TestDispatch_ConcurrentDispatches_DoNotCollide spawns two parallel
// dispatches against different task ids in the same repo. Both must
// land in their own worktree + branch + ledger row.
func TestDispatch_ConcurrentDispatches_DoNotCollide(t *testing.T) {
	f := newDispatchFixture(t)

	// Seed a second task bound to the same agent.
	tk2, err := f.tasks.Enqueue(context.Background(), task.Task{
		WorkspaceID: f.workspaceID,
		ProjectID:   f.projectID,
		StoryID:     f.storyID,
		Kind:        task.KindWork,
		Action:      "contract:develop",
		AgentID:     f.agentID,
		Origin:      task.OriginStoryStage,
		Status:      task.StatusPublished,
	}, f.now)
	if err != nil {
		t.Fatalf("enqueue task 2: %v", err)
	}

	var wg sync.WaitGroup
	results := make([]Result, 2)
	errs := make([]error, 2)
	for i, id := range []string{f.taskID, tk2.ID} {
		wg.Add(1)
		go func(idx int, taskID string) {
			defer wg.Done()
			results[idx], errs[idx] = Dispatch(context.Background(), f.cfg(), f.deps(), taskID, f.agentID)
		}(i, id)
	}
	wg.Wait()

	for i := range results {
		if errs[i] != nil {
			t.Errorf("dispatch %d errored: %v", i, errs[i])
		}
		if !results[i].Success {
			t.Errorf("dispatch %d not successful: error=%q", i, results[i].Error)
		}
	}
	if results[0].Branch == results[1].Branch {
		t.Errorf("expected distinct branches, both got %q", results[0].Branch)
	}
	if results[0].WorktreeDir == results[1].WorktreeDir {
		t.Errorf("expected distinct worktrees, both got %q", results[0].WorktreeDir)
	}
}

// TestDispatch_ModeUnsupported_ReturnsTypedError asserts non-bash modes
// fail with ErrDispatchModeUnsupported and name the supported set in
// the error message.
func TestDispatch_ModeUnsupported_ReturnsTypedError(t *testing.T) {
	f := newDispatchFixture(t)
	cfg := f.cfg()
	cfg.Mode = "team"
	_, err := Dispatch(context.Background(), cfg, f.deps(), f.taskID, f.agentID)
	if err == nil {
		t.Fatal("expected error for unsupported mode")
	}
	if !errors.Is(err, ErrDispatchModeUnsupported) {
		t.Errorf("expected errors.Is ErrDispatchModeUnsupported, got %v", err)
	}
	if !strings.Contains(err.Error(), "supported: bash") {
		t.Errorf("error should name supported modes, got %q", err.Error())
	}
}

// TestDispatch_AgentCannotDeliverAction_Rejected verifies the
// capability check: an agent whose Delivers list omits the task's
// action gets rejected with ErrAgentCannotDeliverAction.
func TestDispatch_AgentCannotDeliverAction_Rejected(t *testing.T) {
	f := newDispatchFixture(t)
	// Seed a second agent that delivers a different contract.
	settings := document.AgentSettings{
		PermissionPatterns: []string{"Read:**"},
		Delivers:           []string{"contract:plan"},
	}
	settingsJSON, _ := document.MarshalAgentSettings(settings)
	other, err := f.docs.Create(context.Background(), document.Document{
		WorkspaceID: f.workspaceID,
		Type:        document.TypeAgent,
		Name:        "test_planner",
		Body:        "planner",
		Structured:  settingsJSON,
		Scope:       document.ScopeSystem,
		Status:      document.StatusActive,
	}, f.now)
	if err != nil {
		t.Fatalf("seed other agent: %v", err)
	}
	_, derr := Dispatch(context.Background(), f.cfg(), f.deps(), f.taskID, other.ID)
	if derr == nil {
		t.Fatal("expected ErrAgentCannotDeliverAction")
	}
	if !errors.Is(derr, ErrAgentCannotDeliverAction) {
		t.Errorf("expected errors.Is ErrAgentCannotDeliverAction, got %v", derr)
	}
}

// TestResolveConfig_Defaults confirms ResolveConfig returns the four
// documented defaults when no KV rows are present in the ledger.
func TestResolveConfig_Defaults(t *testing.T) {
	led := ledger.NewMemoryStore()
	cfg := ResolveConfig(context.Background(), led, ledger.KVResolveOptions{}, nil)
	if cfg.Mode != DefaultMode {
		t.Errorf("Mode default want=%q got=%q", DefaultMode, cfg.Mode)
	}
	if cfg.ClaudePath != DefaultClaudePath {
		t.Errorf("ClaudePath default want=%q got=%q", DefaultClaudePath, cfg.ClaudePath)
	}
	if cfg.TimeoutSeconds != DefaultTimeoutSeconds {
		t.Errorf("TimeoutSeconds default want=%d got=%d", DefaultTimeoutSeconds, cfg.TimeoutSeconds)
	}
	if cfg.PreserveWorktreeOnFailure != DefaultPreserveWorktreeOnFailure {
		t.Errorf("PreserveWorktreeOnFailure default want=%v got=%v", DefaultPreserveWorktreeOnFailure, cfg.PreserveWorktreeOnFailure)
	}
}
