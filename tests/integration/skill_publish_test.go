//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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

const publishTestSkill = `---
name: lib-smoke
description: Smoke skill for the library publish path.
---

# Lib smoke

A test skill promoted to the library.
`

// TestSkillPublishHeadless is the DOGFOOD evidence for sty_d971a478: the
// REAL `satellites skill publish` command, run non-interactively against an
// HTTP server authenticated by a minted API key (no session, no prompt) —
// the exact shape a CI step uses. Publish lands the library row with its
// provenance stamp (publisher, repo URL, commit); --dryrun prints
// identity/version/provenance and leaves the library unchanged; a
// drift-prone skill is blocked by the same content review as upload.
func TestSkillPublishHeadless(t *testing.T) {
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
	ws, err := wsStore.Create(ctx, admin.ID, "publish-ws", now)
	if err != nil {
		t.Fatalf("ws: %v", err)
	}
	if err := wsStore.AddMember(ctx, ws.ID, admin.ID, workspace.RoleAdmin, admin.ID, now); err != nil {
		t.Fatalf("add admin: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "publisher"}, now)
	if err != nil {
		t.Fatalf("pj: %v", err)
	}

	// Mint the project-scoped API key a CI step would hold.
	keyReq, _ := json.Marshal(map[string]any{
		"workspace_id": ws.ID, "project_id": pj.ID, "agent_name": "ci-publisher",
	})
	rawKey, err := verb.Dispatch(auth.WithUser(ctx, admin), "apikey_create", keyReq)
	if err != nil {
		t.Fatalf("apikey_create: %v", err)
	}
	var minted verb.APIKeyCreateResponse
	if err := json.Unmarshal(rawKey, &minted); err != nil {
		t.Fatal(err)
	}

	// A temp repo: satellites.toml bound to the httptest server, the test
	// skill under .satellites/skills/, and a git identity for provenance.
	repo := t.TempDir()
	mustGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	mustGit("init", "-q")
	mustGit("config", "user.email", "ci@example.com")
	mustGit("config", "user.name", "ci")
	mustGit("remote", "add", "origin", "https://github.com/example/skills.git")
	if err := os.MkdirAll(filepath.Join(repo, ".satellites", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	toml := fmt.Sprintf("server_url = %q\nproject_id = %q\n", env.ServerURL, pj.ID)
	tomlPath := filepath.Join(repo, ".satellites", "satellites.toml")
	if err := os.WriteFile(tomlPath, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".satellites", "skills", "lib-smoke.md"), []byte(publishTestSkill), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit("add", "-A")
	mustGit("commit", "-q", "-m", "seed")

	// Headless credentials: the API key in the user-level credential store,
	// exactly where `satellites auth` would put it. No interactive step.
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
		root.SetIn(strings.NewReader("")) // closed stdin: any prompt would fail, proving headless
		root.SetArgs(args)
		err := root.Execute()
		return buf.String(), err
	}

	headSHA := strings.TrimSpace(func() string {
		out, _ := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
		return string(out)
	}())

	libKey := document.Key{Scope: document.ScopeLibrary, ProjectID: pj.ID, Name: "lib-smoke"}

	t.Run("publish lands the library row with provenance", func(t *testing.T) {
		out, err := runCLI("skill", "publish", "lib-smoke", "--config", tomlPath)
		if err != nil {
			t.Fatalf("publish: %v\n%s", err, out)
		}
		res, err := docStore.Get(ctx, libKey, document.GetOptions{})
		if err != nil {
			t.Fatalf("library row not found after publish: %v", err)
		}
		if res.Document.Type != "skill" || res.Document.Audience != "all" {
			t.Fatalf("row shape: type=%s audience=%s", res.Document.Type, res.Document.Audience)
		}
		body := res.Versions[0].Body
		for _, want := range []string{
			"satellites-library:begin",
			`"publisher":"` + pj.ID + `"`,
			`"repo":"https://github.com/example/skills.git"`,
			`"commit":"` + headSHA + `"`,
			"# Lib smoke",
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("published body missing %q:\n%s", want, body)
			}
		}
	})

	t.Run("re-publish advances the version with a single stamp", func(t *testing.T) {
		// Same-content re-publish is an idempotent no-op (upload semantics);
		// a content change is what lands a new version.
		if err := os.WriteFile(filepath.Join(repo, ".satellites", "skills", "lib-smoke.md"),
			[]byte(publishTestSkill+"\nRevised.\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := runCLI("skill", "publish", "lib-smoke", "--config", tomlPath)
		if err != nil {
			t.Fatalf("re-publish: %v\n%s", err, out)
		}
		res, err := docStore.Get(ctx, libKey, document.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if res.Document.LatestVersion != 2 {
			t.Fatalf("latest_version=%d want 2", res.Document.LatestVersion)
		}
		if n := strings.Count(res.Versions[0].Body, "satellites-library:begin"); n != 1 {
			t.Fatalf("stamp count=%d want 1", n)
		}
	})

	t.Run("content review blocks a drift-prone skill before dispatch", func(t *testing.T) {
		drifty := "---\nname: lib-drifty\ndescription: Carries a drift-prone reference.\n---\n\nTracked in sty_deadbeef; see scripts/run-checks.sh for the loop.\n"
		if err := os.WriteFile(filepath.Join(repo, ".satellites", "skills", "lib-drifty.md"), []byte(drifty), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := runCLI("skill", "publish", "lib-drifty", "--config", tomlPath)
		if err == nil || !strings.Contains(out+err.Error(), "content review blocked") {
			t.Fatalf("drift-prone publish not blocked: err=%v\n%s", err, out)
		}
		if _, err := docStore.Get(ctx, document.Key{Scope: document.ScopeLibrary, ProjectID: pj.ID, Name: "lib-drifty"}, document.GetOptions{}); err == nil {
			t.Fatalf("blocked publish still dispatched a row")
		}
	})

	t.Run("missing skill refused with a clear error", func(t *testing.T) {
		_, err := runCLI("skill", "publish", "no-such-skill", "--config", tomlPath)
		if err == nil || !strings.Contains(err.Error(), "no skill at") {
			t.Fatalf("missing-skill publish = %v; want refusal naming the path", err)
		}
	})

	// Last on purpose: the registered cobra command is a process singleton,
	// so a parsed --dryrun would leak into any later Execute in this test.
	t.Run("dryrun previews identity/version/provenance and dispatches nothing", func(t *testing.T) {
		before, err := docStore.Get(ctx, libKey, document.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		out, err := runCLI("skill", "publish", "lib-smoke", "--dryrun", "--config", tomlPath)
		if err != nil {
			t.Fatalf("dryrun: %v\n%s", err, out)
		}
		for _, want := range []string{
			pj.ID + "/lib-smoke",
			fmt.Sprintf("version: %d", before.Document.LatestVersion+1),
			"satellites-library:begin",
			"nothing dispatched",
		} {
			if !strings.Contains(out, want) {
				t.Fatalf("dryrun output missing %q:\n%s", want, out)
			}
		}
		after, err := docStore.Get(ctx, libKey, document.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if after.Document.LatestVersion != before.Document.LatestVersion {
			t.Fatalf("dryrun advanced the version: %d → %d", before.Document.LatestVersion, after.Document.LatestVersion)
		}
	})
}
