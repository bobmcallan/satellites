package cli

import (
	"context"
	"strings"
	"testing"
)

// TestResolveShowTargetEmbed pins sty_34aac909: `workflow show <name>` resolves a
// SYSTEM EMBED workflow by name (no local copy, no server), and an unknown name
// errors naming the embed source.
func TestResolveShowTargetEmbed(t *testing.T) {
	for _, name := range []string{
		"satellites-task-workflow",
		"satellites-baseline-workflow",
		"satellites-parent-workflow",
	} {
		wf, _, err := resolveShowTarget(context.Background(), "", "", name)
		if err != nil {
			t.Fatalf("embed workflow %q should resolve by name: %v", name, err)
		}
		if wf == nil || wf.Name != name {
			t.Fatalf("resolved %q, got %+v", name, wf)
		}
	}

	_, _, err := resolveShowTarget(context.Background(), "", "", "no-such-workflow")
	if err == nil || !strings.Contains(err.Error(), "embed") {
		t.Fatalf("unknown name should error naming the embed source; got %v", err)
	}
}
