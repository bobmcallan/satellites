package server

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestParseLedgerFilter_AllFields(t *testing.T) {
	q := url.Values{}
	q.Set("project_id", "proj_1")
	q.Set("workspace_id", "wksp_1")
	q.Set("session_id", "sess_1")
	q.Set("story_id", "sty_1")
	q.Set("run_id", "run_1")
	q.Set("kind", "log:warn")
	q.Set("body_contains", "boom")
	q.Set("created_after", "2026-05-28T10:00")
	q.Set("created_before", "2026-05-29T10:00")

	in, v := parseLedgerFilter(q)

	if in.ProjectID != "proj_1" || in.WorkspaceID != "wksp_1" || in.SessionID != "sess_1" ||
		in.StoryID != "sty_1" || in.RunID != "run_1" || in.Kind != "log:warn" {
		t.Fatalf("id/kind fields: %+v", in)
	}
	if in.BodyContains != "boom" {
		t.Fatalf("body_contains: %q", in.BodyContains)
	}
	if in.CreatedAfter.IsZero() || in.CreatedBefore.IsZero() {
		t.Fatalf("time bounds: after=%v before=%v", in.CreatedAfter, in.CreatedBefore)
	}
	if v.ProjectID != "proj_1" || v.CreatedAfter != "2026-05-28T10:00" {
		t.Fatalf("view round-trip: %+v", v)
	}
}

func TestParseLedgerFilter_AcceptsRFC3339TimeValues(t *testing.T) {
	q := url.Values{}
	q.Set("created_after", "2026-05-28T10:00:00Z")
	in, _ := parseLedgerFilter(q)
	if in.CreatedAfter.IsZero() {
		t.Fatal("RFC3339 should parse")
	}
}

func TestParseLedgerFilter_IgnoresMalformedTimes(t *testing.T) {
	q := url.Values{}
	q.Set("created_after", "not-a-time")
	in, v := parseLedgerFilter(q)
	if !in.CreatedAfter.IsZero() {
		t.Fatal("malformed time should not set CreatedAfter")
	}
	if v.CreatedAfter != "" {
		t.Fatalf("view CreatedAfter should be empty, got %q", v.CreatedAfter)
	}
}

func TestLedgerListInput_IsEmpty(t *testing.T) {
	if !(ledgerListInput{}).isEmpty() {
		t.Fatal("zero input should be empty")
	}
	if (ledgerListInput{ProjectID: "p"}).isEmpty() {
		t.Fatal("with project should not be empty")
	}
	if (ledgerListInput{BodyContains: "x"}).isEmpty() {
		t.Fatal("with body_contains should not be empty")
	}
}

func TestRenderLedgerEntries_LevelDerivedFromKindPrefix(t *testing.T) {
	rows := []ledgerEntryJSON{
		{ID: "evt_1", Kind: "log:warn", Body: "boom", CreatedAt: time.Now()},
		{ID: "evt_2", Kind: "story_created", Body: "x", CreatedAt: time.Now()},
	}
	out := renderLedgerEntries(rows)
	if len(out) != 2 {
		t.Fatalf("got %d", len(out))
	}
	if out[0].Level != "warn" {
		t.Errorf("log:warn → level=%q", out[0].Level)
	}
	if out[1].Level != "" {
		t.Errorf("story_created → level should be empty, got %q", out[1].Level)
	}
}

func TestRenderLedgerEntries_SourceFirstNonEmpty(t *testing.T) {
	rows := []ledgerEntryJSON{
		{ID: "a", Kind: "log:info", RunID: "run_a", StoryID: "sty_a", CreatedAt: time.Now()},
		{ID: "b", Kind: "log:info", StoryID: "sty_b", CreatedAt: time.Now()},
		{ID: "c", Kind: "log:info", SessionID: "sess_c", CreatedAt: time.Now()},
		{ID: "d", Kind: "log:info", ProjectID: "proj_d", CreatedAt: time.Now()},
		{ID: "e", Kind: "log:info", CreatedAt: time.Now()},
	}
	out := renderLedgerEntries(rows)
	if !strings.HasPrefix(out[0].Source, "run ") {
		t.Errorf("run wins: %q", out[0].Source)
	}
	if !strings.HasPrefix(out[1].Source, "story ") {
		t.Errorf("story when no run: %q", out[1].Source)
	}
	if !strings.HasPrefix(out[2].Source, "session ") {
		t.Errorf("session when no story: %q", out[2].Source)
	}
	if !strings.HasPrefix(out[3].Source, "project ") {
		t.Errorf("project last-resort: %q", out[3].Source)
	}
	if out[4].Source != "" {
		t.Errorf("no ids → empty source, got %q", out[4].Source)
	}
}

func TestTruncateBody(t *testing.T) {
	if got := truncateBody("hello world", 20); got != "hello world" {
		t.Errorf("under limit unchanged: got %q", got)
	}
	if got := truncateBody("hello world this is too long", 10); got != "hello worl…" {
		t.Errorf("over limit truncated with ellipsis: got %q", got)
	}
	if got := truncateBody("first line\nsecond line", 100); strings.Contains(got, "\n") {
		t.Errorf("newlines should be normalised: got %q", got)
	}
}

func TestPrettyJSON_SkipsEmptyDefaults(t *testing.T) {
	if _, ok := prettyJSON([]byte("")); ok {
		t.Error("empty should be skipped")
	}
	if _, ok := prettyJSON([]byte("{}")); ok {
		t.Error("empty object should be skipped")
	}
	if _, ok := prettyJSON([]byte("null")); ok {
		t.Error("null should be skipped")
	}
	if s, ok := prettyJSON([]byte(`{"a":1}`)); !ok || !strings.Contains(s, "\n") {
		t.Errorf("non-empty object pretty-prints: ok=%v out=%q", ok, s)
	}
}

func TestBuildLedgerURL_PreservesFiltersAndSetsCursor(t *testing.T) {
	q := url.Values{}
	q.Set("project_id", "proj_1")
	q.Set("body_contains", "boom")
	q.Set("cursor", "old-cursor") // should be overwritten
	got := buildLedgerURL(q, "next-cursor")
	if !strings.Contains(got, "project_id=proj_1") {
		t.Errorf("project_id preserved: %q", got)
	}
	if !strings.Contains(got, "body_contains=boom") {
		t.Errorf("body_contains preserved: %q", got)
	}
	if !strings.Contains(got, "cursor=next-cursor") {
		t.Errorf("cursor set to next: %q", got)
	}
	if strings.Contains(got, "old-cursor") {
		t.Errorf("old cursor should be replaced: %q", got)
	}
}
