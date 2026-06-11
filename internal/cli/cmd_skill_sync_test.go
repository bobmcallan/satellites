package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/frontmatter"
)

// TestResolveSkillsRoot_AnchorsToRepoNotCWD pins the sty_be65b4dd path fix:
// the materialisation root is the dir that HOLDS .satellites/, derived from the
// resolved satellites.toml — NOT the process CWD. The bug landed skills in
// .satellites/.claude/skills when deploy ran from inside .satellites/; here we
// chdir into .satellites/ and assert the root still resolves to <repo>/.claude/skills.
func TestResolveSkillsRoot_AnchorsToRepoNotCWD(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(repo, "xdg")) // isolate cred store
	satDir := filepath.Join(repo, ".satellites")
	if err := os.MkdirAll(satDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tomlPath := filepath.Join(satDir, "satellites.toml")
	if err := os.WriteFile(tomlPath, []byte("server_url = \"https://x.example\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Run as the buggy invocation did: CWD inside .satellites/.
	t.Chdir(satDir)

	got, err := resolveSkillsRoot("", "")
	if err != nil {
		t.Fatalf("resolveSkillsRoot: %v", err)
	}
	want := filepath.Join(repo, ".claude", "skills")
	if got != want {
		t.Errorf("skills root = %q, want %q (must anchor to repo, not .satellites/ CWD)", got, want)
	}
}

// TestResolveSkillsRoot_ConfigOverrideAndFlag: an explicit --skills-root flag
// wins outright; otherwise a relative skills_root from the TOML resolves
// against the repo root.
func TestResolveSkillsRoot_ConfigOverrideAndFlag(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(repo, "xdg"))
	satDir := filepath.Join(repo, ".satellites")
	if err := os.MkdirAll(satDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tomlPath := filepath.Join(satDir, "satellites.toml")
	if err := os.WriteFile(tomlPath, []byte("server_url = \"https://x.example\"\nskills_root = \"agents/skills\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// TOML relative skills_root → resolved against repo root.
	got, err := resolveSkillsRoot("", tomlPath)
	if err != nil {
		t.Fatalf("resolveSkillsRoot: %v", err)
	}
	if want := filepath.Join(repo, "agents", "skills"); got != want {
		t.Errorf("config-rooted skills root = %q, want %q", got, want)
	}

	// Explicit flag overrides everything, verbatim.
	if got, _ := resolveSkillsRoot("/tmp/explicit", tomlPath); got != "/tmp/explicit" {
		t.Errorf("flag root = %q, want /tmp/explicit", got)
	}
}

// TestStampRoundTrip pins the sty_4b517016 stamp contract sync relies on:
// materialise injects a stamp whose hash is over the authored body only, and
// splitStamp recovers the stamp + the exact authored body, so a freshly
// materialised file reads back as stamped + unedited (hash matches).
func TestStampRoundTrip(t *testing.T) {
	sub := substrateSkill{
		Name:       "fix-workflow",
		DocumentID: "doc_abc123",
		Version:    4,
		Body:       "---\nname: fix-workflow\n---\n# body\nline two\n",
	}
	content := materialise(sub)

	stamp, authored, ok, malformed, legacy := splitStamp(content)
	if !ok || malformed {
		t.Fatalf("materialised file should be cleanly stamped: ok=%v malformed=%v", ok, malformed)
	}
	if legacy {
		t.Error("freshly materialised file must use the frontmatter-first layout, not legacy")
	}
	if stamp.DocumentID != sub.DocumentID || stamp.Version != sub.Version {
		t.Errorf("stamp identity = %+v, want doc_abc123/v4", stamp)
	}
	if authored != sub.Body {
		t.Errorf("authored body not recovered:\n got %q\nwant %q", authored, sub.Body)
	}
	if hashBody(authored) != stamp.Hash {
		t.Errorf("hash of recovered body != stamped hash (would read as edited)")
	}
}

// TestMaterialiseFrontmatterFirst pins the sty_98bf2818 layout contract: a
// materialised SKILL.md begins with the authored YAML frontmatter (so Claude
// Code reads the skill's real description), carries the sync stamp on the
// line immediately after the frontmatter close, and parses cleanly — the
// description is exposed and the stamp never leaks into the parsed body.
func TestMaterialiseFrontmatterFirst(t *testing.T) {
	sub := substrateSkill{
		Name:       "fix-workflow",
		DocumentID: "doc_abc123",
		Version:    4,
		Body:       "---\nname: fix-workflow\ndescription: the real description\n---\n# body\n",
	}
	content := materialise(sub)

	if !strings.HasPrefix(content, "---\n") {
		t.Fatalf("materialised file must start with frontmatter, got %q", content[:40])
	}
	lines := strings.Split(content, "\n")
	stampLine := -1
	for i, l := range lines {
		if strings.HasPrefix(l, stampBegin) {
			stampLine = i
			break
		}
	}
	if stampLine < 0 {
		t.Fatal("materialised file must carry the sync stamp")
	}
	if lines[stampLine-1] != "---" {
		t.Errorf("stamp must sit on the line after the frontmatter close, found after %q", lines[stampLine-1])
	}

	fm, body, err := frontmatter.Parse([]byte(content))
	if err != nil {
		t.Fatalf("frontmatter.Parse on materialised file: %v", err)
	}
	if fm.Description != "the real description" {
		t.Errorf("description not exposed: %q", fm.Description)
	}
	if strings.Contains(string(body), stampBegin) {
		t.Errorf("stamp leaked into the parsed body: %q", body)
	}

	// A body with no frontmatter degrades to stamp-first and still round-trips.
	bare := substrateSkill{Name: "bare", DocumentID: "doc_b", Version: 1, Body: "# no frontmatter\n"}
	st, authored, ok, _, _ := splitStamp(materialise(bare))
	if !ok || authored != bare.Body || st.DocumentID != "doc_b" {
		t.Errorf("frontmatter-less body must still round-trip: ok=%v authored=%q", ok, authored)
	}
}

// TestSplitStampLegacyMigrates pins the migration path: a legacy
// (stamp-at-byte-0) file still reads as stamped with legacy=true, an
// otherwise-current copy reconciles to migrate (one sync repairs it in
// place), and a stamp string mentioned elsewhere — e.g. quoted in a code
// fence — is NOT treated as a stamp.
func TestSplitStampLegacyMigrates(t *testing.T) {
	body := "---\nname: fix-workflow\n---\n# body\n"
	stamp := skillStamp{DocumentID: "doc_abc123", Version: 4, Hash: hashBody(body)}
	legacyContent := stampBlock(stamp) + body

	st, authored, ok, malformed, legacy := splitStamp(legacyContent)
	if !ok || malformed || !legacy {
		t.Fatalf("legacy file must read stamped+legacy: ok=%v malformed=%v legacy=%v", ok, malformed, legacy)
	}
	if authored != body || st.Hash != hashBody(authored) {
		t.Fatalf("legacy authored body not recovered: %q", authored)
	}

	sub := &substrateSkill{Name: "fix-workflow", DocumentID: "doc_abc123", Version: 4, Body: body}
	local := &localSkill{Name: "satellites-fix-workflow", Stamped: true, Stamp: st, Legacy: true, BodyHash: hashBody(authored)}
	if got := reconcileAction(sub, local); got != actionMigrate {
		t.Errorf("current legacy-layout copy should migrate, got %v", got)
	}
	// The same copy in the current layout is simply current.
	local.Legacy = false
	if got := reconcileAction(sub, local); got != actionSkip {
		t.Errorf("current-layout copy should skip, got %v", got)
	}

	// Stamp text inside the body (documentation/code fence) is not a stamp.
	doc := "---\nname: doc\n---\nThe marker looks like:\n```\n" + stampBlock(stamp) + "```\n"
	if _, _, ok, _, _ := splitStamp(doc); ok {
		t.Error("a stamp string quoted in the body must not read as a stamp")
	}
}

// TestReconcileAction covers the five-case stamp-keyed verdict (AC2).
func TestReconcileAction(t *testing.T) {
	sub := &substrateSkill{Name: "s", DocumentID: "doc_1", Version: 2, Body: "b"}
	stampedCurrent := &localSkill{Name: "s", Stamped: true, Stamp: skillStamp{DocumentID: "doc_1", Version: 2, Hash: "h"}, BodyHash: "h"}
	stampedOld := &localSkill{Name: "s", Stamped: true, Stamp: skillStamp{DocumentID: "doc_1", Version: 1, Hash: "h"}, BodyHash: "h"}
	stampedEdited := &localSkill{Name: "s", Stamped: true, Stamp: skillStamp{DocumentID: "doc_1", Version: 2, Hash: "h"}, BodyHash: "DIFFERENT"}
	unstamped := &localSkill{Name: "s", Stamped: false, BodyHash: hashBody("DIFFERENT")}
	unstampedMatch := &localSkill{Name: "s", Stamped: false, BodyHash: hashBody("b")} // identical to sub.Body

	cases := []struct {
		name  string
		sub   *substrateSkill
		local *localSkill
		want  syncAction
	}{
		{"install: substrate, no local", sub, nil, actionInstall},
		{"update: stamped, version advanced, unedited", sub, stampedOld, actionUpdate},
		{"skip: stamped, already current", sub, stampedCurrent, actionSkip},
		{"conflict: stamped but edited", sub, stampedEdited, actionConflict},
		{"conflict: unstamped local + substrate present + body differs", sub, unstamped, actionConflict},
		{"adopt: unstamped local byte-identical to substrate", sub, unstampedMatch, actionMatchUnstamped},
		{"remove: stamped, substrate gone", nil, stampedCurrent, actionRemove},
		{"conflict-on-remove: edited, substrate gone", nil, stampedEdited, actionConflict},
		{"leave: unstamped, substrate gone", nil, unstamped, actionLeaveUnstamped},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := reconcileAction(c.sub, c.local); got != c.want {
				t.Fatalf("reconcileAction = %v, want %v", got, c.want)
			}
		})
	}
}

// TestReconcileSkills_OmitsUnstampedOrphans confirms an operator-authored
// local skill (no stamp, not in substrate) never enters the plan — sync has
// no business with it.
func TestReconcileSkills_OmitsUnstampedOrphans(t *testing.T) {
	subs := []substrateSkill{{Name: "gate", DocumentID: "doc_g", Version: 1, Body: "b"}}
	locals := []localSkill{
		{Name: "satellites-gate", Stamped: true, Stamp: skillStamp{DocumentID: "doc_g", Version: 1, Hash: hashBody("b")}, BodyHash: hashBody("b")},
		{Name: "my-personal-skill", Stamped: false}, // operator-authored
	}
	plan := reconcileSkills(subs, locals, nil)
	for _, item := range plan {
		if item.Name == "my-personal-skill" {
			t.Fatalf("unstamped operator skill must not appear in the sync plan, got action %v", item.Action)
		}
	}
	if len(plan) != 1 || plan[0].Name != "satellites-gate" {
		t.Fatalf("plan = %+v, want only the stamped satellites-gate skill", plan)
	}
}

// TestLocalSkillName pins the prefix-injection helper (AC1): an unprefixed row
// gains the prefix, an already-prefixed one is unchanged (idempotent).
func TestLocalSkillName(t *testing.T) {
	for in, want := range map[string]string{
		"fix-workflow":            "satellites-fix-workflow",
		"satellites-fix-workflow": "satellites-fix-workflow",
		"story_summary":           "satellites-story_summary",
	} {
		if got := localSkillName(in); got != want {
			t.Errorf("localSkillName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestReconcileSkills_InjectsLocalPrefix: an unprefixed substrate row
// materialises under the satellites- prefixed local name (AC1), and a re-sync
// of the already-materialised prefixed copy is a no-op skip (AC2 idempotency).
func TestReconcileSkills_InjectsLocalPrefix(t *testing.T) {
	subs := []substrateSkill{{Name: "fix-workflow", DocumentID: "doc_fw", Version: 1, Body: "b"}}

	// First sync: no local copy → install under the prefixed name.
	plan := reconcileSkills(subs, nil, nil)
	if len(plan) != 1 || plan[0].Name != "satellites-fix-workflow" || plan[0].Action != actionInstall {
		t.Fatalf("plan = %+v, want install satellites-fix-workflow", plan)
	}

	// Second sync: the prefixed, stamped, unedited copy is present → skip.
	locals := []localSkill{{
		Name: "satellites-fix-workflow", Stamped: true,
		Stamp:    skillStamp{DocumentID: "doc_fw", Version: 1, Hash: hashBody("b")},
		BodyHash: hashBody("b"),
	}}
	plan = reconcileSkills(subs, locals, nil)
	if len(plan) != 1 || plan[0].Name != "satellites-fix-workflow" || plan[0].Action != actionSkip {
		t.Fatalf("re-sync plan = %+v, want skip satellites-fix-workflow", plan)
	}
}

// TestMergeByPrecedence_MostSpecificWins pins the union merge (sty_cae81f8c):
// sets are folded low→high (system, workspace, project) keyed on the
// materialised name, so a name owned by more than one scope lands the
// most-specific (project) body on disk.
func TestMergeByPrecedence_MostSpecificWins(t *testing.T) {
	sys := []substrateSkill{
		{Name: "commit-push", DocumentID: "doc_sys", Version: 1, Body: "system body"},
		{Name: "project-setup", DocumentID: "doc_ps", Version: 3, Body: "system project-setup"},
	}
	wsp := []substrateSkill{{Name: "commit-push", DocumentID: "doc_ws", Version: 2, Body: "workspace body"}}
	prj := []substrateSkill{{Name: "commit-push", DocumentID: "doc_prj", Version: 5, Body: "project body"}}

	union := mergeByPrecedence(sys, wsp, prj)
	byName := map[string]substrateSkill{}
	for _, s := range union {
		byName[localSkillName(s.Name)] = s
	}
	if len(byName) != 2 {
		t.Fatalf("union should collapse the shared name to one entry: %d names", len(byName))
	}
	if got := byName["satellites-commit-push"]; got.DocumentID != "doc_prj" || got.Body != "project body" {
		t.Errorf("commit-push winner = %+v, want the project body (most specific)", got)
	}
	if got := byName["satellites-project-setup"]; got.DocumentID != "doc_ps" {
		t.Errorf("system-only project-setup must survive the merge, got %+v", got)
	}
}

// TestReconcileSkills_ProtectedGuardsCrossScopeRemoval pins the union-guarded
// removal (sty_cae81f8c): a stamped local owned by ANOTHER scope (present in
// `protected`) is NOT removed when this scope's substrate omits it — it
// downgrades to skip. A stamped local absent from every scope (not protected)
// is still a true orphan → remove.
func TestReconcileSkills_ProtectedGuardsCrossScopeRemoval(t *testing.T) {
	// This sync reconciles only the system scope's substrate.
	sysSubs := []substrateSkill{{Name: "project-setup", DocumentID: "doc_ps", Version: 3, Body: "ps"}}
	// Local tree: a stamped project skill (owned elsewhere) + a stamped orphan
	// whose substrate row is gone from every scope.
	locals := []localSkill{
		{Name: "satellites-fix-workflow", Stamped: true, Stamp: skillStamp{DocumentID: "doc_fw", Version: 1, Hash: hashBody("fw")}, BodyHash: hashBody("fw")},
		{Name: "satellites-dead-skill", Stamped: true, Stamp: skillStamp{DocumentID: "doc_dead", Version: 1, Hash: hashBody("d")}, BodyHash: hashBody("d")},
	}
	// The union owns the project workflow skill (and the system skill) but NOT
	// the dead one.
	protected := map[string]bool{
		"satellites-project-setup": true,
		"satellites-fix-workflow":  true,
	}

	got := map[string]syncAction{}
	for _, item := range reconcileSkills(sysSubs, locals, protected) {
		got[item.Name] = item.Action
	}
	if got["satellites-project-setup"] != actionInstall {
		t.Errorf("system skill should install, got %v", got["satellites-project-setup"])
	}
	if got["satellites-fix-workflow"] != actionSkip {
		t.Errorf("cross-scope-owned skill must be guarded to skip, got %v", got["satellites-fix-workflow"])
	}
	if got["satellites-dead-skill"] != actionRemove {
		t.Errorf("true orphan (owned by no scope) must still remove, got %v", got["satellites-dead-skill"])
	}
}

// TestListSubstrateSkills_GetKeyedOnRowIDs pins sty_ab160e22 AC2: each per-row
// document_get carries the workspace_id/project_id/scope the list row reports,
// NOT the (empty by default) command flags. document_list skips authorizeRead
// but document_get enforces it and rejects project/workspace scope without a
// workspace_id — so a default-flag `skill sync` must recover the id off the
// row, which document_list already returns.
func TestListSubstrateSkills_GetKeyedOnRowIDs(t *testing.T) {
	const listResp = `{"items":[{"id":"doc_1","name":"gate-a","scope":"project","workspace_id":"wksp_X","project_id":"proj_Y","latest_version":3}]}`
	type getReq struct {
		Name        string `json:"name"`
		Scope       string `json:"scope"`
		WorkspaceID string `json:"workspace_id"`
		ProjectID   string `json:"project_id"`
	}
	var gets []getReq
	dispatch := func(_ context.Context, name string, req json.RawMessage) (json.RawMessage, error) {
		switch name {
		case "document_list":
			return json.RawMessage(listResp), nil
		case "document_get":
			var g getReq
			if err := json.Unmarshal(req, &g); err != nil {
				return nil, err
			}
			gets = append(gets, g)
			return json.RawMessage(`{"raw_body":"---\nname: gate-a\n---\nbody","document":{"id":"doc_1","latest_version":3}}`), nil
		}
		return nil, fmt.Errorf("unexpected verb %q", name)
	}

	// Command-level workspace/project flags are EMPTY — the default-flag
	// invocation that used to fail on the get. The get must still carry the
	// row's ids.
	subs, err := listSubstrateSkills(context.Background(), dispatch, "project", "", "", false)
	if err != nil {
		t.Fatalf("listSubstrateSkills: %v", err)
	}
	if len(gets) != 1 {
		t.Fatalf("expected 1 document_get, got %d", len(gets))
	}
	if g := gets[0]; g.WorkspaceID != "wksp_X" || g.ProjectID != "proj_Y" || g.Scope != "project" {
		t.Fatalf("get not keyed on row ids: %+v (want wksp_X/proj_Y/project)", g)
	}
	if len(subs) != 1 || subs[0].DocumentID != "doc_1" || subs[0].Version != 3 {
		t.Fatalf("subs = %+v, want one row doc_1/v3", subs)
	}
}

// TestApplySyncItem_InstallThenRemove exercises the on-disk apply over a temp
// .claude/skills tree: install writes a stamped SKILL.md; a follow-up
// reconcile against an empty substrate removes it.
func TestApplySyncItem_InstallThenRemove(t *testing.T) {
	root := t.TempDir()
	sub := substrateSkill{Name: "fix-workflow", DocumentID: "doc_fw", Version: 1, Body: "---\nname: fix-workflow\n---\n# fw\n"}

	if err := applySyncItem(root, syncPlanItem{Name: sub.Name, Action: actionInstall, Sub: &sub}); err != nil {
		t.Fatalf("install: %v", err)
	}
	path := filepath.Join(root, "fix-workflow", "SKILL.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("installed file missing: %v", err)
	}
	stamp, authored, ok, _, _ := splitStamp(string(raw))
	if !ok || stamp.DocumentID != "doc_fw" || authored != sub.Body {
		t.Fatalf("installed file not correctly stamped: ok=%v stamp=%+v", ok, stamp)
	}

	// Re-read as a local skill, confirm it reconciles to skip (current) then
	// remove (substrate gone).
	local, err := readLocalSkill(root, "fix-workflow")
	if err != nil || local == nil {
		t.Fatalf("readLocalSkill: %v (nil=%v)", err, local == nil)
	}
	if got := reconcileAction(&sub, local); got != actionSkip {
		t.Errorf("freshly installed copy should skip, got %v", got)
	}
	if got := reconcileAction(nil, local); got != actionRemove {
		t.Errorf("substrate-gone copy should remove, got %v", got)
	}

	if err := applySyncItem(root, syncPlanItem{Name: "fix-workflow", Action: actionRemove}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "fix-workflow")); !os.IsNotExist(err) {
		t.Fatalf("expected dir removed, stat err = %v", err)
	}
}

// TestApplySyncItem_AdoptStampsUnstamped pins sty_ad9e9b4b: an unstamped local
// copy byte-identical to the substrate reconciles to adopt (actionMatchUnstamped),
// and applying it writes the SAME body WITH the identity stamp — so the copy
// becomes managed with no content change, and a re-sync then reads as current.
func TestApplySyncItem_AdoptStampsUnstamped(t *testing.T) {
	root := t.TempDir()
	sub := substrateSkill{Name: "fix-workflow", DocumentID: "doc_fw", Version: 2, Body: "---\nname: fix-workflow\n---\n# fw\n"}
	name := localSkillName(sub.Name)
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Unstamped local copy, byte-identical to the substrate body.
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(sub.Body), 0o644); err != nil {
		t.Fatal(err)
	}

	local, err := readLocalSkill(root, name)
	if err != nil || local == nil {
		t.Fatalf("readLocalSkill: %v (nil=%v)", err, local == nil)
	}
	if local.Stamped {
		t.Fatal("precondition: local copy must be unstamped")
	}
	if got := reconcileAction(&sub, local); got != actionMatchUnstamped {
		t.Fatalf("identical unstamped should adopt (match), got %v", got)
	}

	if err := applySyncItem(root, syncPlanItem{Name: name, Action: actionMatchUnstamped, Sub: &sub}); err != nil {
		t.Fatalf("apply adopt: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatalf("read after adopt: %v", err)
	}
	stamp, authored, ok, _, _ := splitStamp(string(raw))
	if !ok || stamp.DocumentID != "doc_fw" {
		t.Fatalf("adopted file must be stamped: ok=%v stamp=%+v", ok, stamp)
	}
	if authored != sub.Body {
		t.Errorf("adopt must not change the body: got %q want %q", authored, sub.Body)
	}
	// Re-reconcile: now stamped + current → skip.
	local2, _ := readLocalSkill(root, name)
	if got := reconcileAction(&sub, local2); got != actionSkip {
		t.Errorf("after adopt, copy should be current (skip), got %v", got)
	}
}

// TestResolveConflict_ForceBacksUpAndOverwrites: the `force` policy writes the
// current local copy to a .bak (so the operator's edit is never lost) and then
// overwrites SKILL.md with the materialised substrate copy.
func TestResolveConflict_ForceBacksUpAndOverwrites(t *testing.T) {
	root := t.TempDir()
	name := "satellites-fix-workflow"
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	edited := "MY LOCAL EDIT — must survive as a backup\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := &substrateSkill{Name: "fix-workflow", DocumentID: "doc_fw", Version: 3, Body: "---\nname: fix-workflow\n---\n# upstream\n"}

	var out bytes.Buffer
	if err := resolveConflict(&out, nil, conflictForce, root, syncPlanItem{Name: name, Action: actionConflict, Sub: sub}); err != nil {
		t.Fatalf("resolveConflict force: %v", err)
	}

	got, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if string(got) != materialise(*sub) {
		t.Errorf("SKILL.md not overwritten with the substrate copy")
	}
	entries, _ := os.ReadDir(dir)
	var baks []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".bak") {
			baks = append(baks, e.Name())
		}
	}
	if len(baks) != 1 {
		t.Fatalf("want exactly one .bak, got %v", baks)
	}
	bak, _ := os.ReadFile(filepath.Join(dir, baks[0]))
	if string(bak) != edited {
		t.Errorf("backup must preserve the local edit verbatim, got %q", string(bak))
	}
}

// TestResolveConflict_NonInteractivePromptKeepsLocal: with the default
// `prompt` policy but no terminal, the conflict degrades to leave — the local
// edit stays and nothing is backed up or overwritten.
func TestResolveConflict_NonInteractivePromptKeepsLocal(t *testing.T) {
	root := t.TempDir()
	name := "satellites-fix-workflow"
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	edited := "MY LOCAL EDIT\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := &substrateSkill{Name: "fix-workflow", DocumentID: "doc_fw", Version: 3, Body: "upstream"}

	var out bytes.Buffer
	// strings.Reader is not an *os.File → isInteractive is false → degrade.
	if err := resolveConflict(&out, strings.NewReader(""), conflictPrompt, root, syncPlanItem{Name: name, Action: actionConflict, Sub: sub}); err != nil {
		t.Fatalf("resolveConflict prompt(non-tty): %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "SKILL.md")); string(got) != edited {
		t.Errorf("local edit must be kept under a non-interactive prompt, got %q", string(got))
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".bak") {
			t.Errorf("no backup should be written when leaving local, found %s", e.Name())
		}
	}
}

// TestPromptConflict_Choice pins the prompt parsing: an explicit f/force picks
// force; anything else (incl. empty) is the safe leave-local default.
func TestPromptConflict_Choice(t *testing.T) {
	var out bytes.Buffer
	if got := promptConflict(&out, strings.NewReader("f\n"), "x"); got != conflictForce {
		t.Errorf("promptConflict(\"f\") = %q, want force", got)
	}
	if got := promptConflict(&out, strings.NewReader("force\n"), "x"); got != conflictForce {
		t.Errorf("promptConflict(\"force\") = %q, want force", got)
	}
	if got := promptConflict(&out, strings.NewReader("\n"), "x"); got != conflictLocal {
		t.Errorf("promptConflict(empty) = %q, want local", got)
	}
	if got := promptConflict(&out, strings.NewReader("yes\n"), "x"); got != conflictLocal {
		t.Errorf("promptConflict(\"yes\") = %q, want local", got)
	}
}

// TestReconcileActionRehomedRow pins the sty_7994564a re-home fix: a stamped,
// unedited local whose stamp names a DIFFERENT substrate document id (the
// name was re-seeded under a new row, e.g. a project→system scope move with a
// restarted version counter) must update to the new row — a version compare
// across rows is meaningless.
func TestReconcileActionRehomedRow(t *testing.T) {
	body := "---\nname: wf-design\n---\n# body\n"
	sub := &substrateSkill{Name: "wf-design", DocumentID: "doc_new", Version: 1, Body: body}
	local := &localSkill{
		Name:    "satellites-wf-design",
		Stamped: true,
		Stamp:   skillStamp{DocumentID: "doc_old", Version: 3, Hash: hashBody(body)},
		// unedited: on-disk hash equals the stamp hash
		BodyHash: hashBody(body),
	}
	if got := reconcileAction(sub, local); got != actionUpdate {
		t.Errorf("re-homed row must update, got %v", got)
	}
	// Same row, same version: still current.
	local.Stamp.DocumentID = "doc_new"
	local.Stamp.Version = 1
	if got := reconcileAction(sub, local); got != actionSkip {
		t.Errorf("same row current copy should skip, got %v", got)
	}
}
