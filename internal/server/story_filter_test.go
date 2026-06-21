package server

import (
	"testing"
	"time"
)

func mkStory(id, title, status, priority, category string, tags ...string) storyRow {
	return storyRow{ID: id, Title: title, Status: status, Priority: priority, Category: category, Tags: tags}
}

// TestParseStoryQuery pins the grammar port: structured buckets, tags/free
// as slices, unknown key:value falling through to free text.
func TestParseStoryQuery(t *testing.T) {
	q := parseStoryQuery("sty_aaa sty_bbb status:open,done tags:epic:x tags:epic:y order:epic-order priority:high foo:bar")
	if len(q.free) != 3 { // sty_aaa, sty_bbb, foo:bar (unknown key → free)
		t.Errorf("free = %v, want 3 tokens incl. the unknown foo:bar", q.free)
	}
	if len(q.status) != 2 || q.status[0] != "open" || q.status[1] != "done" {
		t.Errorf("status = %v, want [open done]", q.status)
	}
	if len(q.tags) != 2 {
		t.Errorf("tags = %v, want 2 (OR)", q.tags)
	}
	if q.order != "epic-order" {
		t.Errorf("order = %q, want epic-order", q.order)
	}
	if len(q.priority) != 1 || q.priority[0] != "high" {
		t.Errorf("priority = %v, want [high]", q.priority)
	}
	if parseStoryQuery("").isEmpty() != true {
		t.Error("empty query should be isEmpty")
	}
	if parseStoryQuery("status:open").isEmpty() {
		t.Error("status:open is not empty")
	}
}

// TestMatchStory_FreeTextOR pins AC4: multiple free-text tokens OR — a row
// matches when it contains ANY token, so two distinct IDs return both rows.
func TestMatchStory_FreeTextOR(t *testing.T) {
	a := mkStory("sty_25d5b21e", "alpha", "backlog", "high", "feature")
	b := mkStory("sty_0006f5f5", "beta", "ready", "low", "bug")
	c := mkStory("sty_999", "gamma", "backlog", "low", "bug")
	q := parseStoryQuery("sty_25d5b21e sty_0006f5f5 status:all")
	if !matchStory(a, q) {
		t.Error("row A should match (contains its id token)")
	}
	if !matchStory(b, q) {
		t.Error("row B should match (OR over tokens)")
	}
	if matchStory(c, q) {
		t.Error("row C should NOT match — contains neither id")
	}
}

// TestMatchStory_StatusSemantics pins the default (hide done/cancelled),
// status:all (no filter), and open (not done/cancelled).
func TestMatchStory_StatusSemantics(t *testing.T) {
	done := mkStory("s1", "x", "done", "med", "feature")
	open := mkStory("s2", "y", "in_progress", "med", "feature")

	if matchStory(done, parseStoryQuery("")) {
		t.Error("default query must hide done")
	}
	if !matchStory(open, parseStoryQuery("")) {
		t.Error("default query must show non-done")
	}
	if !matchStory(done, parseStoryQuery("status:all")) {
		t.Error("status:all must show done")
	}
	if !matchStory(open, parseStoryQuery("status:open")) {
		t.Error("status:open must show in_progress")
	}
	if matchStory(done, parseStoryQuery("status:open")) {
		t.Error("status:open must hide done")
	}
}

// TestMatchStory_TagsOR pins multi-tag OR (union of memberships).
func TestMatchStory_TagsOR(t *testing.T) {
	a := mkStory("s1", "x", "backlog", "med", "feature", "epic:foo", "area:cli")
	q := parseStoryQuery("tags:epic:foo tags:epic:bar status:all")
	if !matchStory(a, q) {
		t.Error("row in epic:foo should match the OR of foo/bar")
	}
	if matchStory(mkStory("s2", "y", "backlog", "med", "feature", "epic:baz"), q) {
		t.Error("row in neither epic should not match")
	}
}

// TestSortStories_TagOrderSinksMissing pins order:epic-order — natural
// numeric order with missing-tag rows sunk to the bottom (AC6 ordering).
func TestSortStories_TagOrderSinksMissing(t *testing.T) {
	rows := []storyRow{
		mkStory("s10", "x", "backlog", "med", "feature", "epic-order:10"),
		mkStory("sNo", "x", "backlog", "med", "feature"),
		mkStory("s2", "x", "backlog", "med", "feature", "epic-order:2"),
	}
	sortStories(rows, "epic-order")
	if rows[0].ID != "s2" || rows[1].ID != "s10" {
		t.Errorf("natural order want s2,s10 first; got %s,%s", rows[0].ID, rows[1].ID)
	}
	if rows[2].ID != "sNo" {
		t.Errorf("missing-tag row should sink last; got %s", rows[2].ID)
	}
}

// TestSortStories_Title pins a direct column sort (ascending).
func TestSortStories_Title(t *testing.T) {
	rows := []storyRow{
		mkStory("s1", "Zebra", "backlog", "med", "feature"),
		mkStory("s2", "apple", "backlog", "med", "feature"),
	}
	sortStories(rows, "title")
	if rows[0].Title != "apple" {
		t.Errorf("title sort ascending want apple first; got %s", rows[0].Title)
	}
}

// TestSortStories_NoOrderIsStable pins that an empty order leaves the
// server-rendered order intact (no reshuffle).
func TestSortStories_NoOrderIsStable(t *testing.T) {
	now := time.Now()
	rows := []storyRow{
		{ID: "first", CreatedAt: now},
		{ID: "second", CreatedAt: now.Add(-time.Hour)},
	}
	sortStories(rows, "")
	if rows[0].ID != "first" || rows[1].ID != "second" {
		t.Error("empty order must preserve incoming order")
	}
}

// TestFilterStories_PreservesOrder pins that filtering keeps incoming order
// (so a subsequent sort, or the default, is deterministic).
func TestFilterStories_PreservesOrder(t *testing.T) {
	rows := []storyRow{
		mkStory("a", "x", "backlog", "med", "feature"),
		mkStory("b", "y", "done", "med", "feature"),
		mkStory("c", "z", "ready", "med", "feature"),
	}
	got := filterStories(rows, parseStoryQuery("")) // hides done
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "c" {
		t.Errorf("filter want [a c] in order; got %+v", got)
	}
}

func mkTask(id, title, status, category string, tags ...string) taskRow {
	return taskRow{ID: id, Title: title, Status: status, Category: category, Tags: tags}
}

// TestMapTaskStatus pins sty_c54160e1: the standing vocabulary — ready/complete
// (complete is re-runnable) → active, running → running, cancelled → inactive.
func TestMapTaskStatus(t *testing.T) {
	cases := map[string]string{
		"ready": "active", "complete": "active", "running": "running",
		"cancelled": "inactive", "canceled": "inactive", "": "active",
	}
	for in, want := range cases {
		if got := mapTaskStatus(in); got != want {
			t.Errorf("mapTaskStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestFilterTasks covers sty_80447ada AC#4 + sty_c54160e1: tasks reuse the SAME
// story-filter predicate over the MAPPED standing status (active/running/
// inactive). The default open view lists active + running (a complete→active
// task is shown — it is re-runnable, not terminal); only inactive is hidden.
func TestFilterTasks(t *testing.T) {
	all := []taskRow{
		mkTask("tsk_1", "Secrets scan", mapTaskStatus("complete"), "task", "skill:secrets-scan"), // → active
		mkTask("tsk_2", "Build cache warmup", mapTaskStatus("running"), "task", "skill:cache"),   // → running
		mkTask("tsk_3", "Dead task", mapTaskStatus("cancelled"), "task", "skill:misc"),           // → inactive
	}
	total := len(all)

	// Default (empty) query = status:open → lists active + running, hides inactive.
	def := parseStoryQuery("")
	if def.isEmpty() {
		def = parseStoryQuery("status:open")
	}
	open := filterTasks(all, def)
	if len(open) != 2 || open[0].ID != "tsk_1" || open[1].ID != "tsk_2" {
		t.Fatalf("default open filter want [tsk_1 tsk_2] (active+running); got %+v", open)
	}
	if total != 3 {
		t.Errorf("total want 3; got %d", total)
	}

	// status:all → every status, count = total.
	if got := filterTasks(all, parseStoryQuery("status:all")); len(got) != 3 {
		t.Errorf("status:all want 3 rows; got %d", len(got))
	}

	// status:active → only the active (complete→active) task.
	if got := filterTasks(all, parseStoryQuery("status:active")); len(got) != 1 || got[0].ID != "tsk_1" {
		t.Errorf("status:active want [tsk_1]; got %+v", got)
	}

	// Free-text search matches id/title/tags.
	if got := filterTasks(all, parseStoryQuery("status:all secrets")); len(got) != 1 || got[0].ID != "tsk_1" {
		t.Errorf("search 'secrets' want [tsk_1]; got %+v", got)
	}

	// Tag chip filter (the epic-order:1 chips add tags:<tag>).
	if got := filterTasks(all, parseStoryQuery("status:all tags:skill:cache")); len(got) != 1 || got[0].ID != "tsk_2" {
		t.Errorf("tags:skill:cache want [tsk_2]; got %+v", got)
	}
}
