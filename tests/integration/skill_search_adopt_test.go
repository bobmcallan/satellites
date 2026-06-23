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

// TestSkillSearchAdopt is the DOGFOOD evidence for sty_59ad5c0d: against a
// real HTTP server with an API key, `skill search` finds a published test
// skill (same name under two publishers → two distinct rows), `skill adopt`
// forks one into .satellites/skills/ with the forked-from frontmatter stamp
// and no upstream link, overwrite needs --force, and `skill upload` then
// accepts the adopted file through the normal review path as an ordinary
// project skill.
func TestSkillSearchAdopt(t *testing.T) {
	skipInCI(t, "upload subtest needs the claude reviewer, absent on GH runner")
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

	// Two publishers with a same-named library skill, plus the consumer's
	// own project (the repo this CLI session is configured for).
	ws, err := wsStore.Create(ctx, admin.ID, "search-ws", now)
	if err != nil {
		t.Fatalf("ws: %v", err)
	}
	if err := wsStore.AddMember(ctx, ws.ID, admin.ID, workspace.RoleAdmin, admin.ID, now); err != nil {
		t.Fatalf("add admin: %v", err)
	}
	mkProj := func(id, name string) {
		t.Helper()
		if _, err := env.DB.Exec(`INSERT INTO projects (id, workspace_id, name) VALUES ($1, $2, $3)`, id, ws.ID, name); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	mkProj("proj_secteam", "security-team")
	mkProj("proj_devops", "devops-team")
	mkProj("proj_consumer", "consumer-repo")

	publish := func(publisher, headline, body string) {
		t.Helper()
		req, _ := json.Marshal(withSkillReview(map[string]any{
			"type": "skill", "scope": "library",
			"project_id": publisher, "name": "sec-scan",
			"body": body,
		}))
		if _, err := verb.Dispatch(adminCtx, "document_upsert", req); err != nil {
			t.Fatalf("publish %s: %v", publisher, err)
		}
		// The headline verb path is documents-only; stamp the column directly
		// so search's rendering of it is observable.
		if _, err := env.DB.Exec(`UPDATE documents SET headline=$1 WHERE scope='library' AND project_id=$2 AND name='sec-scan'`, headline, publisher); err != nil {
			t.Fatalf("set headline %s: %v", publisher, err)
		}
	}
	publish("proj_secteam",
		"security team's vulnerability scanner",
		"---\nname: sec-scan\ndescription: Gate a release on a dependency vulnerability scan — judge the scan report and decide whether any un-waived HIGH or CRITICAL CVE blocks the ship. Use at release time, once the scan report exists.\nkind: reviewer\n---\n<!-- satellites-library:begin {\"publisher\":\"proj_secteam\",\"repo\":\"https://github.com/example/sec.git\",\"commit\":\"aaa111\"} satellites-library:end -->\n\nYou are the sec-scan release gate. A dependency vulnerability scan report arrives on stdin. Decide ONE thing: is the release free of blocking vulnerabilities?\n\n## Input\nThe scan report on stdin: one row per finding with its severity and fix status.\n\n## Decision rule\n- accept — every HIGH/CRITICAL finding has an applied fix or a recorded, time-boxed waiver; nothing un-triaged remains.\n- reject — any HIGH/CRITICAL finding is un-fixed and un-waived; name each blocking CVE in the notes.\nFail closed: if the report cannot be read or parsed, reject naming why.\n\n## Environment\nYou are a reviewer. You read the report on stdin and write only the verdict — no file or substrate mutation.\n\n```yaml\nguardrails:\n  always:\n    - Judge only the scan report against the block rule; name each blocking CVE on reject.\n  ask_first: []\n  never:\n    - Mutate the tree or write anything but the decision JSON.\n```\n\n## Output\nPrint exactly one JSON object and nothing else: a decision field set to accept or reject, and a notes field of one or two sentences naming each blocking CVE on reject.\n")
	publish("proj_devops",
		"devops flavoured scanner",
		"---\nname: sec-scan\ndescription: Gate a release on a dependency vulnerability scan — judge the scan report and decide whether any un-waived HIGH or CRITICAL CVE blocks the ship. Use at release time, once the scan report exists.\nkind: reviewer\n---\n\n\nYou are the sec-scan release gate. A dependency vulnerability scan report arrives on stdin. Decide ONE thing: is the release free of blocking vulnerabilities?\n\n## Input\nThe scan report on stdin: one row per finding with its severity and fix status.\n\n## Decision rule\n- accept — every HIGH/CRITICAL finding has an applied fix or a recorded, time-boxed waiver; nothing un-triaged remains.\n- reject — any HIGH/CRITICAL finding is un-fixed and un-waived; name each blocking CVE in the notes.\nFail closed: if the report cannot be read or parsed, reject naming why.\n\n## Environment\nYou are a reviewer. You read the report on stdin and write only the verdict — no file or substrate mutation.\n\n```yaml\nguardrails:\n  always:\n    - Judge only the scan report against the block rule; name each blocking CVE on reject.\n  ask_first: []\n  never:\n    - Mutate the tree or write anything but the decision JSON.\n```\n\n## Output\nPrint exactly one JSON object and nothing else: a decision field set to accept or reject, and a notes field of one or two sentences naming each blocking CVE on reject.\n")

	// Consumer repo + headless credentials (API key bound to the consumer
	// project, credential store, satellites.toml).
	keyReq, _ := json.Marshal(map[string]any{
		"workspace_id": ws.ID, "project_id": "proj_consumer", "agent_name": "consumer-agent",
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
	toml := fmt.Sprintf("server_url = %q\nproject_id = %q\n", env.ServerURL, "proj_consumer")
	if err := os.WriteFile(tomlPath, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
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

	adoptedPath := filepath.Join(repo, ".satellites", "skills", "sec-scan.md")

	t.Run("search matches description across publishers as distinct rows", func(t *testing.T) {
		out, err := runCLI("skill", "search", "vulnerability", "--config", tomlPath)
		if err != nil {
			t.Fatalf("search: %v\n%s", err, out)
		}
		var secRow, devRow string
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "proj_secteam") {
				secRow = line
			}
			if strings.Contains(line, "proj_devops") {
				devRow = line
			}
		}
		if secRow == "" || devRow == "" {
			t.Fatalf("expected one row per publisher:\n%s", out)
		}
		for _, row := range []string{secRow, devRow} {
			for _, want := range []string{"sec-scan", "library", "v1"} {
				if !strings.Contains(row, want) {
					t.Fatalf("row missing %q: %s", want, row)
				}
			}
		}
		if !strings.Contains(secRow, "security team's vulnerability scanner") {
			t.Fatalf("headline missing from row: %s", secRow)
		}
	})

	t.Run("adopt writes the fork with provenance and no upstream link", func(t *testing.T) {
		out, err := runCLI("skill", "adopt", "proj_secteam/sec-scan", "--config", tomlPath)
		if err != nil {
			t.Fatalf("adopt: %v\n%s", err, out)
		}
		raw, err := os.ReadFile(adoptedPath)
		if err != nil {
			t.Fatalf("adopted file: %v", err)
		}
		body := string(raw)
		if !strings.Contains(body, "forked-from: proj_secteam/sec-scan@1") {
			t.Fatalf("forked-from stamp missing:\n%s", body)
		}
		if strings.Contains(body, "satellites-library:begin") {
			t.Fatalf("upstream library stamp survived adoption:\n%s", body)
		}
		// The stamp sits inside the frontmatter block.
		if fmEnd := strings.Index(body, "\n---\n"); fmEnd < 0 || strings.Index(body, "forked-from:") > fmEnd {
			t.Fatalf("forked-from not inside frontmatter:\n%s", body)
		}
	})

	t.Run("re-adopt without --force refuses; --force overwrites", func(t *testing.T) {
		_, err := runCLI("skill", "adopt", "proj_secteam/sec-scan", "--config", tomlPath)
		if err == nil || !strings.Contains(err.Error(), "--force") {
			t.Fatalf("overwrite not refused with --force hint: %v", err)
		}
		out, err := runCLI("skill", "adopt", "proj_devops/sec-scan", "--force", "--config", tomlPath)
		if err != nil {
			t.Fatalf("forced adopt: %v\n%s", err, out)
		}
		raw, _ := os.ReadFile(adoptedPath)
		if !strings.Contains(string(raw), "forked-from: proj_devops/sec-scan@1") {
			t.Fatalf("forced adopt did not replace the fork:\n%s", raw)
		}
	})

	t.Run("upload accepts the adopted skill as an ordinary project skill", func(t *testing.T) {
		out, err := runCLI("skill", "upload", "--config", tomlPath)
		if err != nil {
			t.Fatalf("upload: %v\n%s", err, out)
		}
		res, err := docStore.Get(ctx, document.Key{
			Scope: document.ScopeProject, WorkspaceID: ws.ID, ProjectID: "proj_consumer", Name: "sec-scan",
		}, document.GetOptions{})
		if err != nil {
			t.Fatalf("project row after upload: %v", err)
		}
		if res.Document.Type != "skill" {
			t.Fatalf("type=%s want skill", res.Document.Type)
		}
		if !strings.Contains(res.Versions[0].Body, "forked-from: proj_devops/sec-scan@1") {
			t.Fatalf("uploaded body lost the provenance stamp:\n%s", res.Versions[0].Body)
		}
	})
}
