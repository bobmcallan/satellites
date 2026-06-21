package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSkillDispatch stands in for the verb transport: it answers document_list
// (names per scope) and document_get (body by name+scope) from an in-memory
// map keyed by scope, so resolveServerGateBody's precedence + name matching is
// testable with no server. rows[scope] = []{name, body}.
func fakeSkillDispatch(rows map[string][]struct{ Name, Body string }) verbDispatch {
	return func(_ context.Context, name string, req json.RawMessage) (json.RawMessage, error) {
		switch name {
		case "document_list":
			var q struct {
				Scope string `json:"scope"`
			}
			_ = json.Unmarshal(req, &q)
			var items []map[string]any
			for _, r := range rows[q.Scope] {
				items = append(items, map[string]any{"id": "doc_" + r.Name, "name": r.Name, "scope": q.Scope, "latest_version": 1})
			}
			return json.Marshal(map[string]any{"items": items})
		case "document_get":
			var q struct {
				Name, Scope string
			}
			_ = json.Unmarshal(req, &q)
			for _, r := range rows[q.Scope] {
				if r.Name == q.Name {
					return json.Marshal(map[string]any{"raw_body": r.Body, "document": map[string]any{"id": "doc_" + r.Name, "latest_version": 1}})
				}
			}
			return nil, nil
		}
		return json.Marshal(map[string]any{})
	}
}

// emptyConfig writes a minimal valid satellites.toml (no global publishers) so
// listGlobalSkills resolves to an empty library set rather than erroring.
func emptyConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "satellites.toml")
	if err := os.WriteFile(path, []byte("server_url = \"http://localhost:0\"\n\n[measure]\nmode = \"off\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// TestResolveServerGateBody_ScopePrecedence pins sty_b8de4776 AC3: a gate held
// at multiple scopes resolves to the most-specific (project wins over system),
// the SAME precedence skill sync materialises by — so a server hit is the body
// the local cache would have held.
func TestResolveServerGateBody_ScopePrecedence(t *testing.T) {
	rows := map[string][]struct{ Name, Body string }{
		"system":  {{"satellites-custom-review", "SYSTEM BODY"}},
		"project": {{"satellites-custom-review", "PROJECT BODY"}},
	}
	body, ok, err := resolveServerGateBody(context.Background(), fakeSkillDispatch(rows), emptyConfig(t), "ws1", "pj1", "satellites-custom-review")
	if err != nil || !ok {
		t.Fatalf("resolve: ok=%v err=%v", ok, err)
	}
	if string(body) != "PROJECT BODY" {
		t.Fatalf("project scope must win, got %q", body)
	}
}

// TestResolveServerGateBody_PrefixMatch: a row whose source name omits the
// satellites- prefix still resolves under the materialised (prefixed) name the
// gate dispatch asks for.
func TestResolveServerGateBody_PrefixMatch(t *testing.T) {
	rows := map[string][]struct{ Name, Body string }{
		"system": {{"custom-review", "UNPREFIXED ROW BODY"}},
	}
	body, ok, err := resolveServerGateBody(context.Background(), fakeSkillDispatch(rows), emptyConfig(t), "", "", "satellites-custom-review")
	if err != nil || !ok {
		t.Fatalf("resolve: ok=%v err=%v", ok, err)
	}
	if string(body) != "UNPREFIXED ROW BODY" {
		t.Fatalf("expected unprefixed row matched by materialised name, got %q", body)
	}
}

// TestResolveServerGateBody_UnprefixedReference pins sty_832bc70f: a project
// reviewer authored AND referenced under its bare (un-prefixed) name resolves —
// the VIRE case. Before the fix the reference side was compared raw, so a project
// row stored as `vire-planned-review` (materialising as
// `satellites-vire-planned-review`) never matched the bare `vire-planned-review`
// a workflow's reviewer_skill named. Both forms must now resolve the same row.
func TestResolveServerGateBody_UnprefixedReference(t *testing.T) {
	rows := map[string][]struct{ Name, Body string }{
		"project": {{"vire-planned-review", "PROJECT REVIEWER BODY"}},
	}
	for _, ref := range []string{"vire-planned-review", "satellites-vire-planned-review"} {
		body, ok, err := resolveServerGateBody(context.Background(), fakeSkillDispatch(rows), emptyConfig(t), "ws1", "pj1", ref)
		if err != nil || !ok {
			t.Fatalf("resolve %q: ok=%v err=%v", ref, ok, err)
		}
		if string(body) != "PROJECT REVIEWER BODY" {
			t.Fatalf("ref %q: expected project reviewer body, got %q", ref, body)
		}
	}
}

// TestResolveServerGateBody_NotFound: a name no scope holds resolves ok=false
// (not an error) so the dispatcher fails closed naming all three sources.
func TestResolveServerGateBody_NotFound(t *testing.T) {
	rows := map[string][]struct{ Name, Body string }{
		"system": {{"satellites-custom-review", "SYSTEM BODY"}},
	}
	body, ok, err := resolveServerGateBody(context.Background(), fakeSkillDispatch(rows), emptyConfig(t), "", "", "satellites-ghost-review")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok || body != nil {
		t.Fatalf("expected ok=false nil body for absent gate, got ok=%v body=%q", ok, body)
	}
}

// TestServerGateFetcher_ReturnsBodyShape: the wired fetcher returns a body that
// frontmatter.Parse can split (a real SKILL.md), so the dispatcher injects a
// stripped rubric. Sanity that the fetched body is the raw SKILL.md, frontmatter
// intact (the dispatcher strips it).
func TestResolveServerGateBody_RawSkillBody(t *testing.T) {
	skill := "---\nname: satellites-custom-review\nkind: gate\n---\nRUBRIC TEXT\n"
	rows := map[string][]struct{ Name, Body string }{
		"system": {{"satellites-custom-review", skill}},
	}
	body, ok, err := resolveServerGateBody(context.Background(), fakeSkillDispatch(rows), emptyConfig(t), "", "", "satellites-custom-review")
	if err != nil || !ok {
		t.Fatalf("resolve: ok=%v err=%v", ok, err)
	}
	if !strings.HasPrefix(string(body), "---") || !strings.Contains(string(body), "RUBRIC TEXT") {
		t.Fatalf("expected raw SKILL.md body, got %q", body)
	}
}
