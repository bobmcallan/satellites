// Package portalreplicate drives a headless browser (chromedp) against
// any URL, runs a sequence of structured actions, and captures DOM /
// console / screenshot evidence per action. The MCP `portal_replicate`
// tool wraps this package and ledgers each Result onto a story so a
// vague "the X is broken" report becomes a reproducible artefact.
//
// Sty_088f6d5c.
package portalreplicate

import "time"

// ActionType enumerates the canonical actions the runner understands.
// New types require a Go change; the *vocabulary* (alias → canonical)
// is configuration-driven so callers can write natural-language
// sequences. Sty_088f6d5c.
type ActionType string

const (
	// ActionNavigate loads a page. Selector is unused; Value carries
	// the absolute URL (or relative path joined with the runner's
	// base URL).
	ActionNavigate ActionType = "navigate"

	// ActionWaitVisible blocks until Selector resolves to a visible
	// element. TimeoutMs caps the wait; default 5000.
	ActionWaitVisible ActionType = "wait_visible"

	// ActionClick clicks Selector once it is visible. Mirrors
	// chromedp.Click.
	ActionClick ActionType = "click"

	// ActionDOMSnapshot captures the full HTML of Selector (or the
	// document if Selector is empty) into Result.DOM.
	ActionDOMSnapshot ActionType = "dom_snapshot"

	// ActionConsoleCapture rolls up every console message accumulated
	// since the runner started into Result.ConsoleLogs. Subsequent
	// captures dedupe earlier entries — callers see the diff.
	ActionConsoleCapture ActionType = "console_capture"

	// ActionScreenshot captures a PNG of the viewport into
	// Result.Screenshot (raw bytes; the MCP layer base64-encodes for
	// transport).
	ActionScreenshot ActionType = "screenshot"

	// ActionDOMVisible asserts Selector is visible. The action either
	// succeeds (Result.Status="ok") or fails with a clear message —
	// distinct from ActionWaitVisible which is patient.
	ActionDOMVisible ActionType = "dom_visible"
)

// IsKnownAction reports whether t is one of the canonical action
// types. Used by the vocabulary loader to refuse aliases pointing at
// types the runner doesn't implement.
func IsKnownAction(t ActionType) bool {
	switch t {
	case ActionNavigate, ActionWaitVisible, ActionClick,
		ActionDOMSnapshot, ActionConsoleCapture, ActionScreenshot,
		ActionDOMVisible:
		return true
	}
	return false
}

// Action is one step in a replication sequence. Type is canonical
// (callers using natural-language aliases must resolve through a
// Vocabulary first). Selector is a CSS selector when relevant;
// Value carries action-specific data (URL for navigate, text for
// future fill action).
type Action struct {
	Type      ActionType `json:"type"`
	Selector  string     `json:"selector,omitempty"`
	Value     string     `json:"value,omitempty"`
	TimeoutMs int        `json:"timeout_ms,omitempty"`
	// Label is an operator-supplied human-readable name for this
	// action ("open hamburger menu"). Echoed in the Result so the
	// ledger entry is readable without decoding the structured
	// payload. Optional.
	Label string `json:"label,omitempty"`
}

// Cookie is a single cookie injected before navigate. Mirrors the
// subset of network.SetCookie params we expose. Domain + Path
// default to the target URL's host + "/" if empty.
type Cookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain,omitempty"`
	Path     string `json:"path,omitempty"`
	Secure   bool   `json:"secure,omitempty"`
	HTTPOnly bool   `json:"http_only,omitempty"`
}

// Status enumerates per-action outcomes. "ok" = action completed
// without error; "failed" = action raised an error or assertion
// failed; "skipped" = the runner stopped before reaching this action.
type Status string

const (
	StatusOK      Status = "ok"
	StatusFailed  Status = "failed"
	StatusSkipped Status = "skipped"
)

// Result is the per-action outcome captured by the runner. One Result
// per Action; the runner returns []Result in input order, even when an
// earlier action fails (later actions are marked StatusSkipped).
type Result struct {
	Action      Action        `json:"action"`
	Status      Status        `json:"status"`
	Error       string        `json:"error,omitempty"`
	DOM         string        `json:"dom,omitempty"`
	ConsoleLogs []string      `json:"console_logs,omitempty"`
	Screenshot  []byte        `json:"screenshot,omitempty"`
	Duration    time.Duration `json:"duration_ms"`
	StartedAt   time.Time     `json:"started_at"`
}

// Summary rolls up a Run into a single record callers can ledger as a
// single line. Indicates overall pass/fail and the count of each
// status. The full Results slice is also returned so individual rows
// can be ledgered alongside.
type Summary struct {
	TargetURL  string        `json:"target_url"`
	Status     Status        `json:"status"`
	Total      int           `json:"total"`
	Passed     int           `json:"passed"`
	Failed     int           `json:"failed"`
	Skipped    int           `json:"skipped"`
	Duration   time.Duration `json:"duration_ms"`
	StartedAt  time.Time     `json:"started_at"`
	FinishedAt time.Time     `json:"finished_at"`
}
