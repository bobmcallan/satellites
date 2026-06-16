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
	"github.com/spf13/pflag"
)

func batchSkillBody(name, desc string) string {
	return "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n# " + name + "\n\n" + desc + "\n"
}

// TestSkillPublishBatch is the DOGFOOD for sty_a2ad5a95: `skill publish --all`
// and `--changed-since` over a booted server with a minted key — the CI shape.
// --dryrun lists without dispatching; --all publishes each (versions land);
// --changed-since narrows to the changed skill; a bad skill fails the batch
// (non-zero) while the good ones still publish.
func TestSkillPublishBatch(t *testing.T) {
	env := testbootstrap.SetUpWithServer(t)
	ctx := context.Background()
	now := time.Now()

	// The `registered` publish command is a package-level singleton, so flag
	// values set here would leak into other test functions. Reset it to
	// defaults on cleanup (and below before each invocation) so this test does
	// not pollute the shared command tree.
	resetPublishFlags := func() {
		if pub, _, e := cli.NewRootCmd().Find([]string{"skill", "publish"}); e == nil {
			pub.Flags().VisitAll(func(f *pflag.Flag) { _ = f.Value.Set(f.DefValue) })
		}
	}
	t.Cleanup(resetPublishFlags)

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
		t.Fatalf("admin: %v", err)
	}
	ws, err := wsStore.Create(ctx, admin.ID, "batch-ws", now)
	if err != nil {
		t.Fatalf("ws: %v", err)
	}
	if err := wsStore.AddMember(ctx, ws.ID, admin.ID, workspace.RoleAdmin, admin.ID, now); err != nil {
		t.Fatalf("add admin: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{WorkspaceID: ws.ID, Name: "batch-pub"}, now)
	if err != nil {
		t.Fatalf("pj: %v", err)
	}

	keyReq, _ := json.Marshal(map[string]any{"workspace_id": ws.ID, "project_id": pj.ID, "agent_name": "ci-batch"})
	rawResp, err := verb.Dispatch(auth.WithUser(ctx, admin), "apikey_create", keyReq)
	if err != nil {
		t.Fatalf("apikey_create: %v", err)
	}
	var minted verb.APIKeyCreateResponse
	if err := json.Unmarshal(rawResp, &minted); err != nil {
		t.Fatal(err)
	}

	repo := t.TempDir()
	mustGit := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	mustGit("init", "-q")
	mustGit("config", "user.email", "ci@example.com")
	mustGit("config", "user.name", "ci")
	mustGit("remote", "add", "origin", "https://github.com/example/skills.git")
	skillsDir := filepath.Join(repo, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".satellites"), 0o755); err != nil {
		t.Fatal(err)
	}
	tomlPath := filepath.Join(repo, ".satellites", "satellites.toml")
	if err := os.WriteFile(tomlPath, []byte(fmt.Sprintf("server_url = %q\nproject_id = %q\n", env.ServerURL, pj.ID)), 0o644); err != nil {
		t.Fatal(err)
	}
	writeRepoSkill := func(name, desc string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(skillsDir, name+".md"), []byte(batchSkillBody(name, desc)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeRepoSkill("alpha", "First batch skill.")
	writeRepoSkill("beta", "Second batch skill.")
	mustGit("add", "-A")
	mustGit("commit", "-q", "-m", "seed")
	baseBytes, _ := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	base := strings.TrimSpace(string(baseBytes))

	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "xdg"))
	if err := cliconfig.SaveCredential(cliconfig.Credential{
		ServerURL: env.ServerURL, Token: minted.APIKey, KeyID: minted.KeyID, Role: minted.Role,
	}); err != nil {
		t.Fatalf("save credential: %v", err)
	}
	t.Chdir(repo)

	runCLI := func(args ...string) (string, error) {
		// NewRootCmd reuses singleton commands, so cobra flag values persist
		// across in-process Execute calls. A real CLI run is one process — reset
		// the publish flags to defaults so each invocation starts clean.
		resetPublishFlags()
		root := cli.NewRootCmd()
		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetIn(strings.NewReader(""))
		root.SetArgs(append(args, "--config", tomlPath))
		err := root.Execute()
		return buf.String(), err
	}
	libVersion := func(name string) int {
		res, err := docStore.Get(ctx, document.Key{Scope: document.ScopeLibrary, ProjectID: pj.ID, Name: name}, document.GetOptions{})
		if err != nil {
			return 0
		}
		return res.Document.LatestVersion
	}

	// AC4: --all --dryrun lists every skill without dispatching.
	out, err := runCLI("skill", "publish", "--all", "--dryrun")
	if err != nil {
		t.Fatalf("dryrun: %v\n%s", err, out)
	}
	for _, name := range []string{"alpha", "beta"} {
		if !strings.Contains(out, name) {
			t.Fatalf("dryrun output missing %q:\n%s", name, out)
		}
	}
	if libVersion("alpha") != 0 || libVersion("beta") != 0 {
		t.Fatalf("dryrun must not dispatch — library rows should not exist:\n%s", out)
	}

	// AC1: --all publishes every skill (each lands at v1).
	out, err = runCLI("skill", "publish", "--all")
	if err != nil {
		t.Fatalf("publish --all: %v\n%s", err, out)
	}
	if a, b := libVersion("alpha"), libVersion("beta"); a != 1 || b != 1 {
		t.Fatalf("expected both skills at v1, got alpha=%d beta=%d\n%s", a, b, out)
	}

	// AC2: --changed-since publishes only the changed skill.
	writeRepoSkill("alpha", "First batch skill, REVISED.")
	mustGit("add", "-A")
	mustGit("commit", "-q", "-m", "revise alpha")
	out, err = runCLI("skill", "publish", "--changed-since", base)
	if err != nil {
		t.Fatalf("changed-since: %v\n%s", err, out)
	}
	if a, b := libVersion("alpha"), libVersion("beta"); a != 2 || b != 1 {
		t.Fatalf("changed-since should advance only alpha: alpha=%d (want 2) beta=%d (want 1)\n%s", a, b, out)
	}

	// AC2: a --changed-since with no changed skills is a clean no-op (exit 0).
	head, _ := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	out, err = runCLI("skill", "publish", "--changed-since", strings.TrimSpace(string(head)))
	if err != nil {
		t.Fatalf("no-change --changed-since should be a clean no-op, got: %v\n%s", err, out)
	}

	// AC3: a bad skill fails the batch (non-zero) while the good ones publish.
	if err := os.WriteFile(filepath.Join(skillsDir, "broken.md"), []byte(batchSkillBody("not-broken", "mismatched name")), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = runCLI("skill", "publish", "--all")
	if err == nil {
		t.Fatalf("batch with a bad skill should exit non-zero:\n%s", out)
	}
	if !strings.Contains(out, "✗ broken") {
		t.Fatalf("batch should report the failing skill:\n%s", out)
	}
	// The good skills still publish in the same batch (their success lines are
	// present despite the bad one's failure).
	for _, name := range []string{"alpha", "beta"} {
		if !strings.Contains(out, "library/"+pj.ID+"/"+name) {
			t.Fatalf("good skill %q should still publish despite the bad one:\n%s", name, out)
		}
	}
}
