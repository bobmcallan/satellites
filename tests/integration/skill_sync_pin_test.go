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
	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestSkillSyncLibraryPin is the DOGFOOD evidence for sty_56855694: a
// consumer repo pins a published library skill in satellites.toml,
// `skill sync` materialises it into .claude/skills/ with publisher +
// version + document identity in the sync stamp, an upstream publish lands
// on the next sync with the stamp's version advancing, a same-named
// project-scope skill wins precedence on disk, and removing the pin removes
// the materialised copy.
func TestSkillSyncLibraryPin(t *testing.T) {
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
	adminCtx := auth.WithUser(ctx, admin)

	ws, err := wsStore.Create(ctx, admin.ID, "pin-ws", now)
	if err != nil {
		t.Fatalf("ws: %v", err)
	}
	if err := wsStore.AddMember(ctx, ws.ID, admin.ID, workspace.RoleAdmin, admin.ID, now); err != nil {
		t.Fatalf("add admin: %v", err)
	}
	for _, p := range [][2]string{{"proj_pinpub", "pin-publisher"}, {"proj_pincon", "pin-consumer"}} {
		if _, err := env.DB.Exec(`INSERT INTO projects (id, workspace_id, name) VALUES ($1, $2, $3)`, p[0], ws.ID, p[1]); err != nil {
			t.Fatalf("seed %s: %v", p[0], err)
		}
	}

	upsert := func(scope, projectID, name, body string) {
		t.Helper()
		req := map[string]any{
			"type": "skill", "scope": scope,
			"project_id": projectID, "name": name, "body": body,
		}
		if scope == "project" {
			req["workspace_id"] = ws.ID
		}
		raw, _ := json.Marshal(req)
		if _, err := verb.Dispatch(adminCtx, "document_upsert", raw); err != nil {
			t.Fatalf("upsert %s/%s: %v", scope, name, err)
		}
	}

	skillBody := func(name, marker string) string {
		return fmt.Sprintf("---\nname: %s\ndescription: Pinned smoke skill (%s).\nkind: capability\n---\n\n# %s\n\n%s\n", name, marker, name, marker)
	}
	upsert("library", "proj_pinpub", "lib-pin", skillBody("lib-pin", "v1-body"))
	upsert("library", "proj_pinpub", "shared-tool", skillBody("shared-tool", "library-flavour"))
	upsert("project", "proj_pincon", "shared-tool", skillBody("shared-tool", "project-flavour"))

	// Consumer repo with both pins declared + headless credentials.
	keyReq, _ := json.Marshal(map[string]any{
		"workspace_id": ws.ID, "project_id": "proj_pincon", "agent_name": "pin-consumer",
	})
	rawKey, err := verb.Dispatch(adminCtx, "apikey_create", keyReq)
	if err != nil {
		t.Fatalf("apikey_create: %v", err)
	}
	var minted verb.APIKeyCreateResponse
	if err := json.Unmarshal(rawKey, &minted); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".satellites"), 0o755); err != nil {
		t.Fatal(err)
	}
	tomlPath := filepath.Join(repo, ".satellites", "satellites.toml")
	writeToml := func(pins ...string) {
		t.Helper()
		var b strings.Builder
		fmt.Fprintf(&b, "server_url = %q\nproject_id = %q\n", env.ServerURL, "proj_pincon")
		if len(pins) > 0 {
			quoted := make([]string, len(pins))
			for i, p := range pins {
				quoted[i] = fmt.Sprintf("%q", p)
			}
			fmt.Fprintf(&b, "library_pins = [%s]\n", strings.Join(quoted, ", "))
		}
		if err := os.WriteFile(tomlPath, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeToml("proj_pinpub/lib-pin", "proj_pinpub/shared-tool")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "xdg"))
	if err := cliconfig.SaveCredential(cliconfig.Credential{
		ServerURL: env.ServerURL, Token: minted.APIKey, KeyID: minted.KeyID, Role: minted.Role,
	}); err != nil {
		t.Fatalf("save credential: %v", err)
	}
	t.Chdir(repo)

	runCLI := func(args ...string) (string, error) {
		t.Helper()
		root := cli.NewRootCmd()
		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetIn(strings.NewReader(""))
		root.SetArgs(args)
		err := root.Execute()
		return buf.String(), err
	}

	pinnedPath := filepath.Join(repo, ".claude", "skills", "satellites-lib-pin", "SKILL.md")
	sharedPath := filepath.Join(repo, ".claude", "skills", "satellites-shared-tool", "SKILL.md")

	t.Run("sync materialises the pin with publisher+version stamp", func(t *testing.T) {
		out, err := runCLI("skill", "sync", "--config", tomlPath)
		if err != nil {
			t.Fatalf("sync: %v\n%s", err, out)
		}
		raw, err := os.ReadFile(pinnedPath)
		if err != nil {
			t.Fatalf("pinned skill not materialised: %v\n%s", err, out)
		}
		body := string(raw)
		for _, want := range []string{"satellites-sync:begin", `"publisher":"proj_pinpub"`, `"version":1`, `"document_id":"doc_`, "v1-body"} {
			if !strings.Contains(body, want) {
				t.Fatalf("materialised pin missing %q:\n%s", want, body)
			}
		}
	})

	t.Run("project-scope same-name skill wins precedence over the pin", func(t *testing.T) {
		raw, err := os.ReadFile(sharedPath)
		if err != nil {
			t.Fatalf("shared-tool not materialised: %v", err)
		}
		body := string(raw)
		if !strings.Contains(body, "project-flavour") || strings.Contains(body, "library-flavour") {
			t.Fatalf("project scope did not win the collision:\n%s", body)
		}
		if strings.Contains(body, `"publisher"`) {
			t.Fatalf("winning project copy carries a library publisher stamp:\n%s", body)
		}
	})

	t.Run("an upstream publish lands on the next sync with the stamp advancing", func(t *testing.T) {
		upsert("library", "proj_pinpub", "lib-pin", skillBody("lib-pin", "v2-body"))
		out, err := runCLI("skill", "sync", "--config", tomlPath)
		if err != nil {
			t.Fatalf("re-sync: %v\n%s", err, out)
		}
		raw, _ := os.ReadFile(pinnedPath)
		body := string(raw)
		if !strings.Contains(body, "v2-body") || strings.Contains(body, "v1-body") {
			t.Fatalf("upstream update did not land:\n%s", body)
		}
		if !strings.Contains(body, `"version":2`) || !strings.Contains(body, `"publisher":"proj_pinpub"`) {
			t.Fatalf("stamp did not advance with publisher intact:\n%s", body)
		}
	})

	t.Run("removing the pin removes the materialised skill", func(t *testing.T) {
		writeToml("proj_pinpub/shared-tool") // lib-pin unpinned
		out, err := runCLI("skill", "sync", "--config", tomlPath)
		if err != nil {
			t.Fatalf("sync after unpin: %v\n%s", err, out)
		}
		if _, err := os.Stat(pinnedPath); !os.IsNotExist(err) {
			t.Fatalf("unpinned skill still on disk (stat err=%v)\n%s", err, out)
		}
	})

	t.Run("an unresolvable pin fails the sync loudly", func(t *testing.T) {
		writeToml("proj_pinpub/no-such-skill")
		_, err := runCLI("skill", "sync", "--config", tomlPath)
		if err == nil || !strings.Contains(err.Error(), "no-such-skill") {
			t.Fatalf("bad pin did not fail loudly: %v", err)
		}
	})
}
