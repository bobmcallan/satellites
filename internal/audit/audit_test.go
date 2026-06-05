package audit

import "testing"

// TestFindUnengaged_FlagsWorkWithoutEngage: a story with a status_transition but
// no engagement:engage is flagged; a properly-engaged story is not.
func TestFindUnengaged_FlagsWorkWithoutEngage(t *testing.T) {
	entries := []Entry{
		// sty_bad: work happened, never engaged.
		{Story: "sty_bad", Kind: "status_transition", Session: "s1"},
		{Story: "sty_bad", Kind: "commit", Session: "s1"},
		// sty_ok: engaged then worked — clean.
		{Story: "sty_ok", Kind: EngageKind, Session: "s2"},
		{Story: "sty_ok", Kind: "status_transition", Session: "s2"},
		// sty_read: only an engage (e.g. candidate flushed), no work — not flagged.
		{Story: "sty_read", Kind: EngageKind, Session: "s3"},
	}
	got := FindUnengaged(entries)
	if len(got) != 1 {
		t.Fatalf("FindUnengaged = %d findings, want 1: %+v", len(got), got)
	}
	if got[0].Story != "sty_bad" || got[0].Code != CodeUnengagedWork {
		t.Errorf("finding = %+v, want sty_bad/%s", got[0], CodeUnengagedWork)
	}
}

// TestFindUnengaged_Deterministic: multiple anomalies come back sorted by story.
func TestFindUnengaged_Deterministic(t *testing.T) {
	entries := []Entry{
		{Story: "sty_z", Kind: "commit"},
		{Story: "sty_a", Kind: "status_transition"},
		{Story: "sty_m", Kind: "evidence:test"},
	}
	got := FindUnengaged(entries)
	if len(got) != 3 {
		t.Fatalf("want 3 findings, got %d", len(got))
	}
	if got[0].Story != "sty_a" || got[1].Story != "sty_m" || got[2].Story != "sty_z" {
		t.Errorf("findings not sorted by story: %+v", got)
	}
}

// TestFindUnengaged_EngageAloneIsClean: an engage with no work is not an anomaly
// (reading/engaging without working is fine).
func TestFindUnengaged_EngageAloneIsClean(t *testing.T) {
	if got := FindUnengaged([]Entry{{Story: "sty_x", Kind: EngageKind}}); len(got) != 0 {
		t.Errorf("engage-only should be clean, got %+v", got)
	}
}
