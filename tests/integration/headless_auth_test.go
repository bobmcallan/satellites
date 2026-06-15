//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/cli"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestHeadlessAPIKeyAuth is the DOGFOOD evidence for sty_1121f1b2: the REAL
// `satellites exec` command, run against an HTTP server with NO credential
// file, authenticates from the SATELLITES_API_KEY environment variable — the
// exact shape a CI step uses. Unsetting it makes the same call report the clear
// no-api-key error naming both auth paths.
func TestHeadlessAPIKeyAuth(t *testing.T) {
	env := testbootstrap.SetUpWithServer(t)
	ctx := context.Background()
	now := time.Now()

	docStore := document.New(env.DB)
	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	verb.SetDocumentStore(docStore)
	verb.SetAuthStore(env.Store)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	verb.SetLedgerStore(ledger.New(env.DB))
	t.Cleanup(func() {
		verb.SetDocumentStore(nil)
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetLedgerStore(nil)
	})

	admin, err := env.Store.GetUserByEmail(ctx, auth.DevAdminEmail)
	if err != nil {
		t.Fatalf("lookup admin: %v", err)
	}
	ws, err := wsStore.Create(ctx, admin.ID, "headless-ws", now)
	if err != nil {
		t.Fatalf("ws: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "headless-pub"}, now)
	if err != nil {
		t.Fatalf("pj: %v", err)
	}

	// Mint the executor API key a CI step would hold (via the real verb).
	keyReq, _ := json.Marshal(map[string]any{"workspace_id": ws.ID, "project_id": pj.ID, "agent_name": "ci-headless"})
	rawResp, err := verb.Dispatch(auth.WithUser(ctx, admin), "apikey_create", keyReq)
	if err != nil {
		t.Fatalf("apikey_create: %v", err)
	}
	var minted verb.APIKeyCreateResponse
	if err := json.Unmarshal(rawResp, &minted); err != nil {
		t.Fatal(err)
	}

	// A repo TOML bound to the httptest server, and an ISOLATED, EMPTY
	// credential store (no `satellites auth` ever ran).
	repo := t.TempDir()
	tomlPath := filepath.Join(repo, "satellites.toml")
	if err := os.WriteFile(tomlPath, []byte(fmt.Sprintf("server_url = %q\nproject_id = %q\n", env.ServerURL, pj.ID)), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "xdg")) // no credential file

	runExec := func() (string, error) {
		root := cli.NewRootCmd()
		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetIn(strings.NewReader(""))
		root.SetArgs([]string{"exec", "system_status", "--config", tomlPath, "--json", "{}"})
		err := root.Execute()
		return buf.String(), err
	}

	t.Run("SATELLITES_API_KEY authenticates with no credential file", func(t *testing.T) {
		t.Setenv("SATELLITES_API_KEY", minted.APIKey)
		out, err := runExec()
		if err != nil {
			t.Fatalf("exec with env key should succeed, got: %v\n%s", err, out)
		}
		if !strings.Contains(out, `"server"`) || !strings.Contains(out, `"version"`) {
			t.Fatalf("expected a system_status response, got:\n%s", out)
		}
	})

	t.Run("no key + no credential reports the both-paths error", func(t *testing.T) {
		t.Setenv("SATELLITES_API_KEY", "")
		out, err := runExec()
		if err == nil {
			t.Fatalf("exec with no key should fail, got success:\n%s", out)
		}
		combined := out + err.Error()
		if !strings.Contains(combined, "SATELLITES_API_KEY") || !strings.Contains(combined, "satellites auth") {
			t.Fatalf("error should name both auth paths, got:\n%s\nerr=%v", out, err)
		}
	})
}
