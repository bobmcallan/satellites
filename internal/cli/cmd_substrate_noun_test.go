package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestCallerScopedListVia_ConfinesToCallerProject pins the project-scoped
// discovery fix (sty_4add3b87): the default (unflagged) listing — including the
// tag-filtered principle noun — must return only the caller's project rows plus
// the system union, never another project's project-scope rows.
//
// The fake dispatch models the verb's scope confinement: a project-scope
// document_list returns only the rows of the project_id it names (exactly as
// requireScopeKey + addScopePredicate bound it server-side); an empty-scope
// request — the pre-fix leak path — would return every project's rows. Because
// callerScopedListVia names the caller's project_id on its project sub-list, the
// foreign project-B row never appears. No second live project is needed — the
// store is seeded in-test.
func TestCallerScopedListVia_ConfinesToCallerProject(t *testing.T) {
	systemRows := []nounListItem{
		{Name: "agent-goals", Scope: "system", Tags: []string{"principles:global", "principles:always"}},
	}
	storeByProject := map[string][]nounListItem{
		"proj_A": {{Name: "constitution", Scope: "project", Tags: []string{"principles:always"}}},
		"proj_B": {{Name: "foreign-constitution", Scope: "project", Tags: []string{"principles:always"}}},
	}

	var projReqProjectID string
	projRequests := 0
	dispatch := func(_ context.Context, name string, raw json.RawMessage) (json.RawMessage, error) {
		if name != "document_list" {
			return nil, fmt.Errorf("unexpected verb %q", name)
		}
		var req docListRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		var items []nounListItem
		switch req.Scope {
		case "system":
			items = systemRows
		case "project":
			projRequests++
			projReqProjectID = req.ProjectID
			if req.ProjectID == "" {
				// Pre-fix leak: no project predicate → every project's rows.
				for _, rows := range storeByProject {
					items = append(items, rows...)
				}
			} else {
				items = storeByProject[req.ProjectID]
			}
		default:
			// Empty/unscoped — the global leak the fix removes.
			items = append(items, systemRows...)
			for _, rows := range storeByProject {
				items = append(items, rows...)
			}
		}
		return json.Marshal(docListView{Items: items})
	}

	got, err := callerScopedListVia(context.Background(), dispatch, "", "proj_A", "wksp_X", "", nil)
	if err != nil {
		t.Fatalf("callerScopedListVia: %v", err)
	}

	// The project sub-list must name the caller's project — that is what bounds
	// the verb. A request without it would leak (and is the bug under test).
	if projRequests != 1 {
		t.Fatalf("want exactly one project sub-list, got %d", projRequests)
	}
	if projReqProjectID != "proj_A" {
		t.Fatalf("project sub-list project_id = %q, want proj_A (unscoped request leaks foreign rows)", projReqProjectID)
	}

	names := make([]string, len(got))
	for i, it := range got {
		names[i] = it.Name
	}
	if !contains(names, "agent-goals") {
		t.Errorf("system union dropped: %v", names)
	}
	if !contains(names, "constitution") {
		t.Errorf("caller-project row dropped: %v", names)
	}
	if contains(names, "foreign-constitution") {
		t.Errorf("foreign project row leaked into the listing: %v", names)
	}
}

func TestFilterByTagPrefix(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	items := []nounListItem{
		{Name: "global-1", Tags: []string{"principles:global"}, UpdatedAt: now, Scope: "system"},
		{Name: "project-1", Tags: []string{"principles:project"}, UpdatedAt: now, Scope: "project"},
		{Name: "unrelated", Tags: []string{"kind:misc"}, UpdatedAt: now, Scope: "system"},
		{Name: "no-tags", Tags: nil, UpdatedAt: now, Scope: "system"},
	}
	got := filterByTagPrefix(items, "principles:")
	if len(got) != 2 {
		t.Fatalf("expected 2 principle rows, got %d: %+v", len(got), got)
	}
	names := []string{got[0].Name, got[1].Name}
	if !contains(names, "global-1") || !contains(names, "project-1") {
		t.Errorf("missing expected names: %+v", names)
	}
}

func TestFilterByTagPrefix_EmptyPassesThrough(t *testing.T) {
	items := []nounListItem{
		{Name: "a", Scope: "system"},
		{Name: "b", Scope: "system"},
	}
	got := filterByTagPrefix(items, "")
	if len(got) != 2 {
		t.Fatalf("expected all rows when prefix is empty, got %d", len(got))
	}
}

func TestRenderNounList_EmptyMessage(t *testing.T) {
	var buf bytes.Buffer
	renderNounList(&buf, nil)
	if !strings.Contains(buf.String(), "no rows") {
		t.Fatalf("expected no-rows message, got %q", buf.String())
	}
}

func TestRenderNounList_HeadersAndRow(t *testing.T) {
	var buf bytes.Buffer
	renderNounList(&buf, []nounListItem{{
		Name:          "story_reviewer",
		Scope:         "system",
		Type:          "skill",
		LatestVersion: 3,
		UpdatedAt:     time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC),
	}})
	got := buf.String()
	for _, want := range []string{"NAME", "SCOPE", "VERSION", "UPDATED", "story_reviewer", "system", "3"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q; got:\n%s", want, got)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("truncate short: %q", got)
	}
	if got := truncate("abcdefghij", 5); got != "abcd…" {
		t.Errorf("truncate long: %q", got)
	}
}

func TestNewSubstrateListCmd_RequestShape(t *testing.T) {
	// Verify the cobra command parses flags and the underlying request
	// shape is what we expect. We don't dispatch — just construct the
	// cmd and confirm it builds.
	configArg, userArg := "", ""
	cmd := newSubstrateListCmd(substrateNounConfig{
		Use:        "list",
		Short:      "list skills",
		FilterType: "skill",
		ConfigArg:  &configArg,
		UserArg:    &userArg,
	})
	if cmd.Use != "list" {
		t.Errorf("cmd.Use = %q, want list", cmd.Use)
	}
	if cmd.Flag("scope") == nil {
		t.Error("expected --scope flag")
	}
	if cmd.Flag("workspace") == nil {
		t.Error("expected --workspace flag")
	}
	if cmd.Flag("project") == nil {
		t.Error("expected --project flag")
	}
}

func TestNewSubstrateGetCmd_ArgsRequired(t *testing.T) {
	configArg, userArg := "", ""
	cmd := newSubstrateGetCmd(substrateNounConfig{
		Use:        "get",
		Short:      "get skill",
		FilterType: "skill",
		ConfigArg:  &configArg,
		UserArg:    &userArg,
	})
	if err := cmd.Args(cmd, []string{}); err == nil {
		t.Error("expected error on zero args")
	}
	if err := cmd.Args(cmd, []string{"name"}); err != nil {
		t.Errorf("expected ok on one arg, got %v", err)
	}
	if err := cmd.Args(cmd, []string{"name", "extra"}); err == nil {
		t.Error("expected error on two args")
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
