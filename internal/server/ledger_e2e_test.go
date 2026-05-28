package server

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/correlation"
	"github.com/bobmcallan/satellites/internal/ledger"
)

// e2eFakeStore is a minimal in-memory AppendManyer used by the end-
// to-end test below. Kept here (not imported from internal/ledger
// tests) to honour the convention that test helpers in one package
// stay private to that package.
type e2eFakeStore struct {
	mu      sync.Mutex
	batches [][]ledger.AppendInput
}

func (s *e2eFakeStore) AppendMany(_ context.Context, ins []ledger.AppendInput, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := append([]ledger.AppendInput(nil), ins...)
	s.batches = append(s.batches, cp)
	return nil
}

func (s *e2eFakeStore) firstRow() (ledger.AppendInput, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.batches) == 0 || len(s.batches[0]) == 0 {
		return ledger.AppendInput{}, false
	}
	return s.batches[0][0], true
}

// TestEndToEnd_HeaderToContextToArborToLedger covers AC10:
// "an arbor.WarnCtx inside an HTTP handler with run-id header lands
// as a ledger row carrying every tag."
func TestEndToEnd_HeaderToContextToArborToLedger(t *testing.T) {
	store := &e2eFakeStore{}
	h := ledger.NewHandler(store, ledger.HandlerOptions{
		MinLevel:      slog.LevelDebug,
		BatchSize:     1,
		BatchInterval: 10 * time.Millisecond,
	})
	defer h.Close(context.Background())

	// Swap the process-global logger to one that writes through this
	// handler. Restore the original at test end so other tests are
	// unaffected.
	prev := arbor.Default()
	arbor.SetHandlers(slog.LevelDebug, h)
	t.Cleanup(func() { arbor.SetDefault(prev) })

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arbor.WarnCtx(r.Context(), "e2e probe", "k", "v")
		w.WriteHeader(http.StatusOK)
	})
	mux := correlationMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set(correlation.HeaderRunID, "run_e2e")
	req.Header.Set(correlation.HeaderSessionID, "sess_e2e")
	req.Header.Set(correlation.HeaderStoryID, "sty_e2e")
	req.Header.Set(correlation.HeaderProjectID, "proj_e2e")
	req.Header.Set(correlation.HeaderWorkspaceID, "wksp_e2e")
	mux.ServeHTTP(httptest.NewRecorder(), req)

	deadline := time.Now().Add(500 * time.Millisecond)
	var row ledger.AppendInput
	for time.Now().Before(deadline) {
		if r, ok := store.firstRow(); ok {
			row = r
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if row.Kind == "" {
		t.Fatal("ledger received no row")
	}
	if row.Kind != "log:warn" {
		t.Errorf("kind: got %q want log:warn", row.Kind)
	}
	if row.Body != "e2e probe" {
		t.Errorf("body: got %q", row.Body)
	}
	if row.RunID != "run_e2e" {
		t.Errorf("run_id: got %q", row.RunID)
	}
	if row.SessionID != "sess_e2e" {
		t.Errorf("session_id: got %q", row.SessionID)
	}
	if row.StoryID != "sty_e2e" {
		t.Errorf("story_id: got %q", row.StoryID)
	}
	if row.ProjectID != "proj_e2e" {
		t.Errorf("project_id: got %q", row.ProjectID)
	}
	if row.WorkspaceID != "wksp_e2e" {
		t.Errorf("workspace_id: got %q", row.WorkspaceID)
	}
}

// TestEndToEnd_NoHeadersNoLedgerRow confirms the deliberate cutoff:
// server background work with no correlation context produces no
// ledger noise.
func TestEndToEnd_NoHeadersNoLedgerRow(t *testing.T) {
	store := &e2eFakeStore{}
	h := ledger.NewHandler(store, ledger.HandlerOptions{
		MinLevel:      slog.LevelDebug,
		BatchSize:     1,
		BatchInterval: 10 * time.Millisecond,
	})
	defer h.Close(context.Background())

	prev := arbor.Default()
	arbor.SetHandlers(slog.LevelDebug, h)
	t.Cleanup(func() { arbor.SetDefault(prev) })

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arbor.WarnCtx(r.Context(), "probe with no headers")
	})
	mux := correlationMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	mux.ServeHTTP(httptest.NewRecorder(), req)

	time.Sleep(100 * time.Millisecond)
	if _, ok := store.firstRow(); ok {
		t.Fatal("expected no row when no correlation headers present")
	}
}
