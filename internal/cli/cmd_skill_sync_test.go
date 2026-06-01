package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

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

	stamp, authored, ok, malformed := splitStamp(content)
	if !ok || malformed {
		t.Fatalf("materialised file should be cleanly stamped: ok=%v malformed=%v", ok, malformed)
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

// TestReconcileAction covers the five-case stamp-keyed verdict (AC2).
func TestReconcileAction(t *testing.T) {
	sub := &substrateSkill{Name: "s", DocumentID: "doc_1", Version: 2, Body: "b"}
	stampedCurrent := &localSkill{Name: "s", Stamped: true, Stamp: skillStamp{DocumentID: "doc_1", Version: 2, Hash: "h"}, BodyHash: "h"}
	stampedOld := &localSkill{Name: "s", Stamped: true, Stamp: skillStamp{DocumentID: "doc_1", Version: 1, Hash: "h"}, BodyHash: "h"}
	stampedEdited := &localSkill{Name: "s", Stamped: true, Stamp: skillStamp{DocumentID: "doc_1", Version: 2, Hash: "h"}, BodyHash: "DIFFERENT"}
	unstamped := &localSkill{Name: "s", Stamped: false}

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
		{"leave: unstamped local + substrate present", sub, unstamped, actionLeaveUnstamped},
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
	plan := reconcileSkills(subs, locals)
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
	plan := reconcileSkills(subs, nil)
	if len(plan) != 1 || plan[0].Name != "satellites-fix-workflow" || plan[0].Action != actionInstall {
		t.Fatalf("plan = %+v, want install satellites-fix-workflow", plan)
	}

	// Second sync: the prefixed, stamped, unedited copy is present → skip.
	locals := []localSkill{{
		Name: "satellites-fix-workflow", Stamped: true,
		Stamp:    skillStamp{DocumentID: "doc_fw", Version: 1, Hash: hashBody("b")},
		BodyHash: hashBody("b"),
	}}
	plan = reconcileSkills(subs, locals)
	if len(plan) != 1 || plan[0].Name != "satellites-fix-workflow" || plan[0].Action != actionSkip {
		t.Fatalf("re-sync plan = %+v, want skip satellites-fix-workflow", plan)
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
	subs, err := listSubstrateSkills(context.Background(), dispatch, "project", "", "")
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
	stamp, authored, ok, _ := splitStamp(string(raw))
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
