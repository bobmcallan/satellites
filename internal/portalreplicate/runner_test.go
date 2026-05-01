package portalreplicate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// TestRun_NavigateClickSnapshot is the substrate-level happy-path
// test for sty_088f6d5c: spin up a tiny in-process HTML page with a
// JS-driven toggle, run navigate → click → dom_snapshot via the
// runner, and assert the snapshot reflects the post-click DOM. Gated
// on chromium availability — skips cleanly when no browser is
// reachable (CI without chromium installed, sandboxed environments).
func TestRun_NavigateClickSnapshot(t *testing.T) {
	if !chromiumAvailable(t) {
		t.Skip("chromium unavailable")
	}

	const page = `<!DOCTYPE html><html><body>
<button id="toggle" onclick="document.getElementById('panel').classList.toggle('open')">Toggle</button>
<div id="panel">closed</div>
<script>
document.getElementById('toggle').addEventListener('click', () => {
    const p = document.getElementById('panel');
    p.textContent = p.classList.contains('open') ? 'open' : 'closed';
});
</script>
</body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	results, summary, err := Run(ctx, RunOptions{TargetURL: srv.URL, Headless: true}, []Action{
		{Type: ActionNavigate},
		{Type: ActionWaitVisible, Selector: "#toggle"},
		{Type: ActionClick, Selector: "#toggle"},
		{Type: ActionDOMSnapshot, Selector: "#panel"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.Status != StatusOK {
		t.Fatalf("summary.Status = %q, want ok; results=%+v", summary.Status, results)
	}
	if summary.Passed != 4 || summary.Failed != 0 {
		t.Errorf("summary passed/failed = %d/%d, want 4/0", summary.Passed, summary.Failed)
	}
	if len(results) != 4 {
		t.Fatalf("results len = %d, want 4", len(results))
	}
	dom := results[3].DOM
	if !strings.Contains(dom, "open") {
		t.Errorf("dom_snapshot did not capture post-click state; DOM = %q", dom)
	}
}

// TestRun_FailedActionStopsAndSkips verifies the contract that a
// failed action stops execution and downstream actions get
// StatusSkipped Results — the caller sees the whole sequence shape
// rather than a truncated array.
func TestRun_FailedActionStopsAndSkips(t *testing.T) {
	if !chromiumAvailable(t) {
		t.Skip("chromium unavailable")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!DOCTYPE html><html><body><h1>hi</h1></body></html>`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	results, summary, err := Run(ctx, RunOptions{TargetURL: srv.URL, Headless: true}, []Action{
		{Type: ActionNavigate},
		{Type: ActionWaitVisible, Selector: "#nonexistent", TimeoutMs: 250},
		{Type: ActionClick, Selector: "#also-nonexistent"},
		{Type: ActionDOMSnapshot},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.Status != StatusFailed {
		t.Fatalf("summary.Status = %q, want failed", summary.Status)
	}
	if results[0].Status != StatusOK {
		t.Errorf("navigate result status = %q, want ok", results[0].Status)
	}
	if results[1].Status != StatusFailed {
		t.Errorf("wait_visible (missing selector) status = %q, want failed", results[1].Status)
	}
	for i := 2; i < len(results); i++ {
		if results[i].Status != StatusSkipped {
			t.Errorf("results[%d].Status = %q, want skipped", i, results[i].Status)
		}
	}
}

// chromiumAvailable probes the chromedp allocator with a no-op Run
// so a missing or unlaunchable chromium fails the call and we skip.
// Mirrors tests/portalui/chrome.go's gate.
func chromiumAvailable(t *testing.T) bool {
	t.Helper()
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	defer allocCancel()
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()
	return chromedp.Run(browserCtx) == nil
}
