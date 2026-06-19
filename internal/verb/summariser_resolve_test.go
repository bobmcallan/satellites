package verb

import (
	"context"
	"strings"
	"testing"
)

// TestResolveSkillEmbedLocalServer_ServerFallback pins sty_27bcfe75 AC3: the
// shared embed→local→server resolver (used by both the gate dispatcher and the
// summariser) fetches a skill body from the server when it is absent from the
// worktree .claude/skills, fails closed when no fetcher is wired, and errors
// when no source holds the name.
func TestResolveSkillEmbedLocalServer_ServerFallback(t *testing.T) {
	body := "---\nname: cap\nkind: capability\n---\n# cap body\n"
	fetch := func(_ context.Context, name string) ([]byte, bool, error) {
		if name == "cap" {
			return []byte(body), true, nil
		}
		return nil, false, nil
	}

	// Absent local + fetch hit → server body, frontmatter parsed.
	fm, b, err := resolveSkillEmbedLocalServer(context.Background(), fetch, t.TempDir(), "cap")
	if err != nil {
		t.Fatalf("server fallback: %v", err)
	}
	if fm.Name != "cap" || !strings.Contains(b, "cap body") {
		t.Fatalf("resolved fm=%+v body=%q", fm, b)
	}

	// Nil fetch → fail closed (the local-miss error is surfaced, not recovered).
	if _, _, err := resolveSkillEmbedLocalServer(context.Background(), nil, t.TempDir(), "cap"); err == nil {
		t.Fatal("nil fetch must fail closed on a local miss")
	}

	// Fetch reports no scope holds the name → no-source error.
	if _, _, err := resolveSkillEmbedLocalServer(context.Background(), fetch, t.TempDir(), "absent"); err == nil {
		t.Fatal("a name no source holds must error")
	}
}
