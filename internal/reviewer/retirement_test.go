package reviewer_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestReviewerServiceRetired asserts that internal/reviewer/service has
// been deleted (sty_51571015 retirement). The package was the in-process
// listener that auto-claimed kind:review tasks and wrote verdicts
// directly. Reviews now route through the orchestrator session, which
// dispatches the appropriate reviewer agent via `agent_dispatch`
// (internal/agentdispatch.Dispatch). This is the negative-test the AC
// asks for: prove the listener no longer exists; reviews land only via
// agent_dispatch.
func TestReviewerServiceRetired(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed — cannot resolve repo root")
	}
	servicePath := filepath.Join(filepath.Dir(thisFile), "service")
	if st, err := os.Stat(servicePath); !os.IsNotExist(err) {
		mode := "<unknown>"
		if st != nil {
			mode = st.Mode().String()
		}
		t.Fatalf("internal/reviewer/service must be deleted (sty_51571015) but was found: %s (mode=%s, err=%v)",
			servicePath, mode, err)
	}
}
