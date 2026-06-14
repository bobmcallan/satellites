package verb

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/bobmcallan/satellites/internal/document"
)

// taskSpecFromSkillBody strips a leading frontmatter block and satellites stamp
// comment lines, leaving the instruction markdown a run feeds the generator.
func TestTaskSpecFromSkillBody(t *testing.T) {
	body := "---\nname: corpus-summarise\nkind: task\n---\n" +
		"<!-- satellites-sync:begin {\"x\":1} satellites-sync:end -->\n\n" +
		"# Corpus summarise\n\nDo the thing.\n"
	if got, want := taskSpecFromSkillBody(body), "# Corpus summarise\n\nDo the thing."; got != want {
		t.Fatalf("frontmatter+stamp strip:\n got=%q\nwant=%q", got, want)
	}
	if got := taskSpecFromSkillBody("\n\njust text\n"); got != "just text" {
		t.Fatalf("plain body: got=%q want=%q", got, "just text")
	}
	if got := taskSpecFromSkillBody("---\nonly: frontmatter\n---\n"); got != "" {
		t.Fatalf("empty spec: got=%q want empty", got)
	}
}

func TestWorkspaceTaskVerbsRegistered(t *testing.T) {
	for _, n := range []string{"workspace_task_run", "workspace_task_upsert", "workspace_task_list"} {
		if Get(n) == nil {
			t.Errorf("verb %q not registered", n)
		}
	}
}

// resolveTaskSkillKey validates scope and the project_id requirement, and keys
// each scope correctly.
func TestResolveTaskSkillKey(t *testing.T) {
	ws := "wksp_1"
	k, err := resolveTaskSkillKey(taskSkillRef{Scope: "workspace", Name: "t"}, ws)
	if err != nil || k.WorkspaceID != ws || k.Scope != document.ScopeWorkspace {
		t.Fatalf("workspace scope: key=%+v err=%v", k, err)
	}
	k, err = resolveTaskSkillKey(taskSkillRef{Scope: "library", Name: "t", ProjectID: "proj_1"}, ws)
	if err != nil || k.ProjectID != "proj_1" || k.WorkspaceID != "" {
		t.Fatalf("library scope: key=%+v err=%v", k, err)
	}
	for _, bad := range []taskSkillRef{
		{Scope: "library", Name: "t"},  // missing project_id
		{Scope: "workspace"},           // missing name
		{Scope: "nonsense", Name: "t"}, // unsupported scope
		{Name: "t"},                    // missing scope
	} {
		if _, err := resolveTaskSkillKey(bad, ws); !errors.Is(err, ErrBadRequest) {
			t.Errorf("ref %+v: want ErrBadRequest, got %v", bad, err)
		}
	}
}

// Input validation runs before the admin gate (authStore nil bypasses it) and
// before any store write, so a bad request never reaches the store.
func TestWorkspaceTaskUpsertValidation(t *testing.T) {
	prevDoc, prevAuth := documentStore, authStore
	documentStore, authStore = &document.Store{}, nil
	defer func() { documentStore, authStore = prevDoc, prevAuth }()

	good := taskSkillRef{Scope: "library", Name: "corpus-summarise", ProjectID: "proj_1"}
	cases := []WorkspaceTaskUpsertRequest{
		{Name: "t", TaskSkill: good, OutputName: "o"},                                        // missing workspace_id
		{WorkspaceID: "w", TaskSkill: good, OutputName: "o"},                                 // missing name
		{WorkspaceID: "w", Name: "a/b", TaskSkill: good, OutputName: "o"},                    // name has '/'
		{WorkspaceID: "w", Name: "t", TaskSkill: good},                                       // missing output_name
		{WorkspaceID: "w", Name: "t", TaskSkill: good, OutputName: "o", Trigger: "schedule"}, // unsupported trigger
		{WorkspaceID: "w", Name: "t", OutputName: "o"},                                       // bad skill ref (no scope/name)
	}
	for i, c := range cases {
		raw, _ := json.Marshal(c)
		if _, err := invokeWorkspaceTaskUpsert(context.Background(), raw); !errors.Is(err, ErrBadRequest) {
			t.Errorf("case %d (%+v): want ErrBadRequest, got %v", i, c, err)
		}
	}
}

// The agent tool dispatcher refuses any tool outside the allowlist before
// reaching the verb registry.
func TestWorkspaceAgentToolsRefusesUnknown(t *testing.T) {
	_, disp := workspaceAgentTools("ws1")
	if _, err := disp(context.Background(), "document_delete", json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected refusal of a non-allowlisted tool")
	}
}

// forceArgs forces the workspace scoping over anything the model supplies — the
// agent cannot read another workspace by passing its own workspace_id.
func TestForceArgs(t *testing.T) {
	out, err := forceArgs(map[string]string{"workspace_id": "ws_real", "scope": "workspace"},
		json.RawMessage(`{"name":"x","workspace_id":"ws_EVIL"}`))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["workspace_id"] != "ws_real" || got["scope"] != "workspace" || got["name"] != "x" {
		t.Fatalf("forced args wrong: %+v", got)
	}
}
