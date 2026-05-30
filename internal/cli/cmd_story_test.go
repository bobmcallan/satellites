package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestRunStory_StreamLinesLandAsLedgerRows covers sty_7af47a91 AC5/8:
// a stub claude binary emits a known stream-json sequence; the driver
// runs it; the test asserts every line landed as a ledger_append POST
// carrying the expected correlation headers and kind.
func TestRunStory_StreamLinesLandAsLedgerRows(t *testing.T) {
	// Stub claude — a shell script printing three lines on stdout and
	// one warning on stderr, then exiting 0.
	stubDir := t.TempDir()
	stub := filepath.Join(stubDir, "claude-stub.sh")
	script := `#!/bin/sh
echo '{"type":"system","subtype":"init"}'
echo '{"type":"message","role":"assistant","content":"hello"}'
echo 'warning: throttled' >&2
echo '{"type":"result","subtype":"done"}'
exit 0
`
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	// Capture every server call so the test can assert both the kind
	// and the headers each ledger row carries.
	type seen struct {
		Verb    string
		Headers http.Header
		Body    json.RawMessage
	}
	var (
		mu     sync.Mutex
		calls  []seen
		called = func(v string, h http.Header, b []byte) {
			mu.Lock()
			defer mu.Unlock()
			cp := h.Clone()
			calls = append(calls, seen{Verb: v, Headers: cp, Body: json.RawMessage(append([]byte(nil), b...))})
		}
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /api/v1/exec/<verb_name>
		verbName := strings.TrimPrefix(r.URL.Path, "/api/v1/exec/")
		body, _ := io.ReadAll(r.Body)
		called(verbName, r.Header, body)
		w.Header().Set("Content-Type", "application/json")
		switch verbName {
		case "document_get":
			// Return a story envelope matching the project the test
			// expects.
			_, _ = w.Write([]byte(`{"document":{"id":"sty_test","type":"story","project_id":"proj_t","workspace_id":"wksp_t","name":"test"}}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	// Point the CLI at the stub server via env-var config override.
	// cliconfig.Load walks env vars first (SATELLITES_CONFIG), and the
	// loaded config carries ServerURL + Auth.Token + project_id; we
	// write a temp TOML and point at it.
	tomlPath := filepath.Join(stubDir, "satellites.toml")
	toml := "server_url = \"" + srv.URL + "\"\n" +
		"project_id = \"proj_t\"\n" +
		"[auth]\ntoken = \"tok-test\"\n"
	if err := os.WriteFile(tomlPath, []byte(toml), 0o600); err != nil {
		t.Fatalf("write toml: %v", err)
	}

	// Clear inherited env vars so the test isolates header generation.
	for _, p := range correlationEnvHeaders {
		t.Setenv(p.Env, "")
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	err := runStory(context.Background(), runStoryOpts{
		StoryID:    "sty_test",
		ConfigPath: tomlPath,
		ClaudeBin:  stub,
		Stdout:     &stdoutBuf,
		Stderr:     &stderrBuf,
		Spawn:      defaultSpawn,
	})
	if err != nil {
		t.Fatalf("runStory: %v (stderr=%q)", err, stderrBuf.String())
	}

	// Inspect the captured server calls.
	mu.Lock()
	defer mu.Unlock()
	var (
		gotDocumentGet bool
		stream         []seen
		stderrRows     []seen
		startRow       *seen
		exitRow        *seen
	)
	for i := range calls {
		c := calls[i]
		switch c.Verb {
		case "document_get":
			gotDocumentGet = true
		case "ledger_append":
			var req struct {
				Kind string `json:"kind"`
				Body string `json:"body"`
			}
			if err := json.Unmarshal(c.Body, &req); err != nil {
				t.Fatalf("decode ledger_append body: %v", err)
			}
			switch req.Body {
			case "claude:start":
				startRow = &calls[i]
			case "claude:exit":
				exitRow = &calls[i]
			case "claude:stream":
				stream = append(stream, c)
			case "claude:stderr":
				stderrRows = append(stderrRows, c)
			}
			if req.Kind != "log:info" && req.Kind != "log:warn" {
				t.Errorf("unexpected ledger kind %q", req.Kind)
			}
		}
	}

	if !gotDocumentGet {
		t.Error("expected document_get for the story")
	}
	if startRow == nil {
		t.Error("expected a claude:start row")
	}
	if exitRow == nil {
		t.Error("expected a claude:exit row")
	}
	if len(stream) != 3 {
		t.Errorf("expected 3 stdout stream rows, got %d", len(stream))
	}
	if len(stderrRows) != 1 {
		t.Errorf("expected 1 stderr row, got %d", len(stderrRows))
	}

	// Every ledger_append must carry all five correlation headers.
	wantHeaders := []string{
		"X-Satellites-Run-Id", "X-Satellites-Session-Id",
		"X-Satellites-Story-Id", "X-Satellites-Project-Id",
		"X-Satellites-Workspace-Id",
	}
	for _, c := range append(stream, stderrRows...) {
		for _, h := range wantHeaders {
			if v := c.Headers.Get(h); v == "" {
				t.Errorf("%s missing %s header (got body=%s)", c.Verb, h, string(c.Body))
			}
		}
	}

	// The exit row's payload carries exit_code=0 (stub exited 0).
	if exitRow != nil {
		var req struct {
			Payload map[string]any `json:"payload"`
		}
		if err := json.Unmarshal(exitRow.Body, &req); err != nil {
			t.Fatalf("decode exit body: %v", err)
		}
		if got, ok := req.Payload["exit_code"]; !ok || got == nil {
			t.Errorf("exit payload missing exit_code: %v", req.Payload)
		}
	}
}

func TestRunStory_RefusesNonStoryDocument(t *testing.T) {
	stubDir := t.TempDir()
	tomlPath := filepath.Join(stubDir, "satellites.toml")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Pretend it's a type:"document" row, not a story.
		_, _ = w.Write([]byte(`{"document":{"id":"doc_x","type":"document","project_id":"proj_t","workspace_id":"wksp_t"}}`))
	}))
	defer srv.Close()
	toml := "server_url = \"" + srv.URL + "\"\nproject_id = \"proj_t\"\n[auth]\ntoken=\"t\"\n"
	if err := os.WriteFile(tomlPath, []byte(toml), 0o600); err != nil {
		t.Fatalf("write toml: %v", err)
	}
	err := runStory(context.Background(), runStoryOpts{
		StoryID:    "doc_x",
		ConfigPath: tomlPath,
		ClaudeBin:  "/bin/true",
		Stdout:     io.Discard,
		Stderr:     io.Discard,
		Spawn:      defaultSpawn,
	})
	if err == nil || !strings.Contains(err.Error(), "type=\"story\"") {
		t.Fatalf("expected refusal on non-story document, got %v", err)
	}
}

func TestRunStory_RefusesCrossProject(t *testing.T) {
	stubDir := t.TempDir()
	tomlPath := filepath.Join(stubDir, "satellites.toml")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"document":{"id":"sty_other","type":"story","project_id":"proj_other","workspace_id":"wksp_t"}}`))
	}))
	defer srv.Close()
	toml := "server_url = \"" + srv.URL + "\"\nproject_id = \"proj_t\"\n[auth]\ntoken=\"t\"\n"
	if err := os.WriteFile(tomlPath, []byte(toml), 0o600); err != nil {
		t.Fatalf("write toml: %v", err)
	}
	err := runStory(context.Background(), runStoryOpts{
		StoryID:    "sty_other",
		ConfigPath: tomlPath,
		ClaudeBin:  "/bin/true",
		Stdout:     io.Discard,
		Stderr:     io.Discard,
		Spawn:      defaultSpawn,
	})
	if err == nil || !strings.Contains(err.Error(), "belongs to project") {
		t.Fatalf("expected cross-project refusal, got %v", err)
	}
}

func TestMintCorrelationID_PrefixedHex(t *testing.T) {
	id := mintCorrelationID("run")
	if !strings.HasPrefix(id, "run_") {
		t.Errorf("prefix: %q", id)
	}
	if len(id) != len("run_")+8 {
		t.Errorf("length: %q has %d chars (want %d)", id, len(id), len("run_")+8)
	}
}

func TestRenderRunPrompt_NamesIDsAndPointsAtVerbs(t *testing.T) {
	p := renderRunPrompt(storyEnvelope{
		ID: "sty_abc", ProjectID: "proj_def", WorkspaceID: "wksp_ghi",
	}, "")
	for _, want := range []string{"sty_abc", "proj_def", "wksp_ghi", "document_get", "story review sty_abc"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q:\n%s", want, p)
		}
	}
	// The gate is client-side: it reads the workflow skill from the local
	// worktree, which the server can't see. The prompt must NOT point the
	// executor at the server-side story_request_review verb (sty_523e727d).
	if strings.Contains(p, "story_request_review") {
		t.Errorf("prompt drives the gate via the server-side story_request_review verb, which cannot read the local worktree skill:\n%s", p)
	}
}

// Defensive — ensure the spawn seam is wired and a stub spawn from a
// test can replace the default.
func TestSpawn_DefaultIsExec(t *testing.T) {
	cmd, err := defaultSpawn(context.Background(), "/bin/true", []string{"-x"}, []string{"K=V"})
	if err != nil {
		t.Fatalf("defaultSpawn: %v", err)
	}
	if _, ok := any(cmd).(*exec.Cmd); !ok {
		t.Fatal("expected *exec.Cmd")
	}
}
