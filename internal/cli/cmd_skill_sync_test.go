package cli

import (
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
		{Name: "gate", Stamped: true, Stamp: skillStamp{DocumentID: "doc_g", Version: 1, Hash: hashBody("b")}, BodyHash: hashBody("b")},
		{Name: "my-personal-skill", Stamped: false}, // operator-authored
	}
	plan := reconcileSkills(subs, locals)
	for _, item := range plan {
		if item.Name == "my-personal-skill" {
			t.Fatalf("unstamped operator skill must not appear in the sync plan, got action %v", item.Action)
		}
	}
	if len(plan) != 1 || plan[0].Name != "gate" {
		t.Fatalf("plan = %+v, want only the stamped gate skill", plan)
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
