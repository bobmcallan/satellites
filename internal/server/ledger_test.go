package server

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestParseLedgerSearch_KeyValueTokens(t *testing.T) {
	in := parseLedgerSearch("project_id:proj_1 run_id:run_a kind:log:warn")
	if in.ProjectID != "proj_1" || in.RunID != "run_a" {
		t.Fatalf("ids: %+v", in)
	}
	if in.Kind != "log:warn" {
		t.Fatalf("kind preserves embedded colons: %q", in.Kind)
	}
	if len(in.BodyContainsAny) != 0 {
		t.Fatalf("no free text expected, got %v", in.BodyContainsAny)
	}
}

func TestParseLedgerSearch_FreeTextOrSemantics(t *testing.T) {
	in := parseLedgerSearch("Principle initialisation project_id:proj_fc7d72d8")
	if in.ProjectID != "proj_fc7d72d8" {
		t.Errorf("project_id: %q", in.ProjectID)
	}
	if !slicesHave(in.BodyContainsAny, "Principle", "initialisation") {
		t.Errorf("free text → BodyContainsAny: got %v", in.BodyContainsAny)
	}
}

func TestParseLedgerSearch_UnknownKeyFallsThroughToBody(t *testing.T) {
	in := parseLedgerSearch("unknownkey:value")
	if !slicesHave(in.BodyContainsAny, "unknownkey:value") {
		t.Fatalf("unknown key:value is body-search: got %v", in.BodyContainsAny)
	}
}

func TestParseLedgerSearch_Empty(t *testing.T) {
	if !parseLedgerSearch("   ").isEmpty() {
		t.Fatal("blank search is empty")
	}
	if !parseLedgerSearch("").isEmpty() {
		t.Fatal("empty search is empty")
	}
}

func TestParseFlexibleTime_RFC3339AndShortForm(t *testing.T) {
	if _, ok := parseFlexibleTime("2026-05-28T10:00:00Z"); !ok {
		t.Error("RFC3339 should parse")
	}
	if _, ok := parseFlexibleTime("2026-05-28T10:00"); !ok {
		t.Error("HTML datetime-local should parse")
	}
	if _, ok := parseFlexibleTime("not a time"); ok {
		t.Error("garbage should not parse")
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

// TestRenderLedgerEntries_StepSummaryInline pins sty_2517f6b8 AC3: a
// step_summary row is marked IsSummary with a "summary" badge level and
// keeps its prose in BodyFull, so the template renders it inline (visible)
// rather than only in the expand panel. A non-summary row stays unmarked.
func TestRenderLedgerEntries_StepSummaryInline(t *testing.T) {
	rows := []ledgerEntryJSON{
		{ID: "evt_s", Kind: "step_summary", Body: "Started feature X; plan accepted.", CreatedAt: time.Now()},
		{ID: "evt_t", Kind: "status_transition", Body: "ready → in_progress", CreatedAt: time.Now()},
	}
	out := renderLedgerEntries(rows)
	if len(out) != 2 {
		t.Fatalf("got %d", len(out))
	}
	if !out[0].IsSummary {
		t.Errorf("step_summary row should be IsSummary")
	}
	if out[0].Level != "summary" {
		t.Errorf("step_summary level = %q, want summary", out[0].Level)
	}
	if out[0].BodyFull != "Started feature X; plan accepted." {
		t.Errorf("step_summary body not preserved for inline render: %q", out[0].BodyFull)
	}
	if out[1].IsSummary {
		t.Errorf("status_transition row must not be marked IsSummary")
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

func TestBuildLedgerURL_PreservesSearchAndTimesAndSetsCursor(t *testing.T) {
	q := url.Values{}
	q.Set("q", "Principle project_id:proj_fc7d72d8")
	q.Set("created_after", "2026-05-28T10:00")
	q.Set("cursor", "old-cursor") // should be overwritten
	got := buildLedgerURL(q, "next-cursor")
	if !strings.Contains(got, "q=") {
		t.Errorf("q preserved: %q", got)
	}
	if !strings.Contains(got, "created_after=") {
		t.Errorf("created_after preserved: %q", got)
	}
	if !strings.Contains(got, "cursor=next-cursor") {
		t.Errorf("cursor set to next: %q", got)
	}
	if strings.Contains(got, "old-cursor") {
		t.Errorf("old cursor should be replaced: %q", got)
	}
}

func slicesHave(haystack []string, needles ...string) bool {
	for _, n := range needles {
		found := false
		for _, h := range haystack {
			if h == n {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
