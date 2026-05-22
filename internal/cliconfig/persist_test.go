package cliconfig

import (
	"strings"
	"testing"
)

func TestRewriteProjectIDLine_Replaces(t *testing.T) {
	in := []byte(`server_url = "https://x"
project_id = "proj_old"
repo_path = "."

[auth]
token = "sk_x"
`)
	out, err := rewriteProjectIDLine(in, "proj_new")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, `project_id = "proj_new"`) {
		t.Fatalf("missing new id: %s", got)
	}
	if strings.Contains(got, "proj_old") {
		t.Fatalf("old id not replaced: %s", got)
	}
	if !strings.Contains(got, `[auth]`) || !strings.Contains(got, `token = "sk_x"`) {
		t.Fatalf("auth section damaged: %s", got)
	}
}

func TestRewriteProjectIDLine_InsertsBeforeFirstSection(t *testing.T) {
	in := []byte(`server_url = "https://x"
repo_path = "."

[auth]
token = "sk_x"
`)
	out, err := rewriteProjectIDLine(in, "proj_new")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, `project_id = "proj_new"`) {
		t.Fatalf("missing new id: %s", got)
	}
	// Inserted line must precede [auth].
	pidIdx := strings.Index(got, "project_id")
	authIdx := strings.Index(got, "[auth]")
	if pidIdx < 0 || authIdx < 0 || pidIdx >= authIdx {
		t.Fatalf("project_id not before [auth]: pid=%d auth=%d body=%s", pidIdx, authIdx, got)
	}
}

func TestRewriteProjectIDLine_AppendsWhenNoSections(t *testing.T) {
	in := []byte(`server_url = "https://x"
repo_path = "."
`)
	out, err := rewriteProjectIDLine(in, "proj_new")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.HasSuffix(strings.TrimRight(got, "\n"), `project_id = "proj_new"`) {
		t.Fatalf("project_id not appended at EOF: %s", got)
	}
}

func TestRewriteProjectIDLine_LeavesCommentedFormAlone(t *testing.T) {
	in := []byte(`server_url = "https://x"
# project_id = "proj_old_commented"

[auth]
token = "sk_x"
`)
	out, err := rewriteProjectIDLine(in, "proj_new")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, `# project_id = "proj_old_commented"`) {
		t.Fatalf("commented form was modified: %s", got)
	}
	if !strings.Contains(got, `project_id = "proj_new"`) {
		t.Fatalf("new uncommented project_id missing: %s", got)
	}
}

func TestRewriteProjectIDLine_TolerantOfNoSpace(t *testing.T) {
	in := []byte(`server_url = "https://x"
project_id="proj_old"

[auth]
token = "sk_x"
`)
	out, err := rewriteProjectIDLine(in, "proj_new")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, `project_id = "proj_new"`) {
		t.Fatalf("missing new id: %s", got)
	}
	if strings.Contains(got, "proj_old") {
		t.Fatalf("old id not replaced: %s", got)
	}
}
