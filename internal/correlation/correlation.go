// Package correlation carries operator-observability ids on
// context.Context. The HTTP middleware reads the X-Satellites-* request
// headers and stamps an IDs value onto the per-request context; arbor's
// LedgerHandler reads the same value when persisting log events as
// ledger rows, so every log inside a tagged request lands with the
// run/session/story/project/workspace correlation set the operator
// can filter on.
//
// The ids are entirely informational — nothing in the verb pipeline
// behaves differently when they are missing. They exist so the portal
// `/ledger` page (sty_becb7019) can isolate one `claude -p` run
// (sty_7af47a91) from another after the fact.
package correlation

import "context"

// HTTPHeader names. A caller that exports the matching env vars (e.g. on
// a spawned claude subprocess) has satellites-client
// forward them as headers on every outgoing HTTP call to
// satellites-server, which this package's middleware lifts onto the
// request context.
const (
	HeaderRunID       = "X-Satellites-Run-ID"
	HeaderSessionID   = "X-Satellites-Session-ID"
	HeaderStoryID     = "X-Satellites-Story-ID"
	HeaderProjectID   = "X-Satellites-Project-ID"
	HeaderWorkspaceID = "X-Satellites-Workspace-ID"
)

// IDs is the per-request correlation snapshot. Empty strings mean
// "no id at this scope"; that's the canonical absence marker.
type IDs struct {
	RunID       string
	SessionID   string
	StoryID     string
	ProjectID   string
	WorkspaceID string
}

// Empty reports whether every id is blank. The LedgerHandler skips
// writes for empty-context log records — observability is gated on a
// run/session/story being explicitly named, so server background work
// (boot, migrations, periodic ticks) does not fill the ledger.
func (i IDs) Empty() bool {
	return i.RunID == "" && i.SessionID == "" && i.StoryID == "" &&
		i.ProjectID == "" && i.WorkspaceID == ""
}

// Context key. Unexported so callers must go through the helpers; one
// key holds the full IDs struct rather than five separate keys so the
// reader path is a single map lookup.
type ctxKey struct{}

var idsKey = ctxKey{}

// WithIDs returns a new context carrying ids. Empty ids are still
// stamped (callers may later check FromContext for the value).
func WithIDs(ctx context.Context, ids IDs) context.Context {
	return context.WithValue(ctx, idsKey, ids)
}

// FromContext returns the IDs attached to ctx, or the zero value when
// none were stamped. The second return reports whether anything was
// stamped — handy for distinguishing "never set" from "set to zero".
func FromContext(ctx context.Context) (IDs, bool) {
	v, ok := ctx.Value(idsKey).(IDs)
	if !ok {
		return IDs{}, false
	}
	return v, true
}
