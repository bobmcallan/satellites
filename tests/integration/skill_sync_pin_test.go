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

// TestSkillSyncGlobalPublishers is the DOGFOOD evidence for
// epic:client-dir-separation order-4 (sty_c206a8d0): a consumer repo OPTS INTO a
// publisher via global_publishers and `skill sync` materialises ALL of that
// publisher's global (library) artifacts into .claude/skills/ with the
// publisher+version sync stamp; an upstream publish lands on the next sync with
// the stamp advancing; a same-named project-scope skill wins precedence;
// removing the publisher removes its materialised copies; and the RETIRED
// library_pins still works as a deprecated back-compat fallback (its publishers
// are derived). Replaces the per-skill library_pins dogfood (sty_56855694).
func TestSkillSyncGlobalPublishers(t *testing.T) {
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
		raw, _ := json.Marshal(withSkillReview(req))
		if _, err := verb.Dispatch(adminCtx, "document_upsert", raw); err != nil {
			t.Fatalf("upsert %s/%s: %v", scope, name, err)
		}
	}

	// sync:true opts the skill into local materialisation (sty_36894714, default
	// false). A publisher tags a skill the consumer's agent invokes via the Skill
	// tool so `skill sync` pulls it to .claude/skills.
	skillBody := func(name, marker string) string {
		return fmt.Sprintf("---\nname: %s\ndescription: Pinned smoke skill (%s).\nkind: capability\ntags: [sync:true]\n---\n\n# %s\n\n%s\n", name, marker, name, marker)
	}
	upsert("library", "proj_pinpub", "lib-pin", skillBody("lib-pin", "v1-body"))
	upsert("library", "proj_pinpub", "shared-tool", skillBody("shared-tool", "library-flavour"))
	upsert("project", "proj_pincon", "shared-tool", skillBody("shared-tool", "project-flavour"))

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
	// writeToml writes the consumer config. A "global_publishers=" line opts into
	// publishers; a "library_pins=" line exercises the deprecated fallback.
	writeToml := func(line string) {
		t.Helper()
		body := fmt.Sprintf("server_url = %q\nproject_id = %q\n%s", env.ServerURL, "proj_pincon", line)
		if err := os.WriteFile(tomlPath, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeToml("global_publishers = [\"proj_pinpub\"]\n")
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

	t.Run("global_publishers materialises a publisher's library skills with publisher+version stamp", func(t *testing.T) {
		out, err := runCLI("skill", "sync", "--config", tomlPath)
		if err != nil {
			t.Fatalf("sync: %v\n%s", err, out)
		}
		raw, err := os.ReadFile(pinnedPath)
		if err != nil {
			t.Fatalf("global skill not materialised: %v\n%s", err, out)
		}
		body := string(raw)
		for _, want := range []string{"satellites-sync:begin", `"publisher":"proj_pinpub"`, `"version":1`, `"document_id":"doc_`, "v1-body"} {
			if !strings.Contains(body, want) {
				t.Fatalf("materialised global skill missing %q:\n%s", want, body)
			}
		}
	})

	t.Run("project-scope same-name skill wins precedence over the global", func(t *testing.T) {
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

	t.Run("removing the publisher removes its materialised skills", func(t *testing.T) {
		writeToml("global_publishers = []\n")
		out, err := runCLI("skill", "sync", "--config", tomlPath)
		if err != nil {
			t.Fatalf("sync after opt-out: %v\n%s", err, out)
		}
		if _, err := os.Stat(pinnedPath); !os.IsNotExist(err) {
			t.Fatalf("opted-out global skill still on disk (stat err=%v)\n%s", err, out)
		}
		// The project-scope shared-tool stays — it is the repo's own, not global.
		if _, err := os.Stat(sharedPath); err != nil {
			t.Fatalf("project-scope shared-tool must survive an opt-out: %v", err)
		}
	})

	t.Run("deprecated library_pins still consumes (publishers derived) with a note", func(t *testing.T) {
		writeToml("library_pins = [\"proj_pinpub/lib-pin\"]\n")
		out, err := runCLI("skill", "sync", "--config", tomlPath)
		if err != nil {
			t.Fatalf("sync with deprecated pins: %v\n%s", err, out)
		}
		if !strings.Contains(out, "deprecated") {
			t.Fatalf("a library_pins-only config must print the deprecation note:\n%s", out)
		}
		if _, err := os.Stat(pinnedPath); err != nil {
			t.Fatalf("derived publisher must still materialise lib-pin: %v\n%s", err, out)
		}
	})
}
