package server

import (
	"net/http"

	"github.com/bobmcallan/satellites/internal/correlation"
)

// correlationMiddleware lifts the X-Satellites-* request headers onto
// the request context so downstream verb handlers (and the arbor
// LedgerHandler that taps slog records) can tag ledger rows with the
// run/session/story/project/workspace ids the calling `claude -p`
// driver stamped.
//
// All headers are optional. A request with no headers passes through
// with an empty IDs value still on the context — that's the signal to
// the LedgerHandler to skip logging (server background work isn't
// per-run observable). Setting at least one header opts the request
// into ledger-backed log capture.
func correlationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ids := correlation.IDs{
			RunID:       r.Header.Get(correlation.HeaderRunID),
			SessionID:   r.Header.Get(correlation.HeaderSessionID),
			StoryID:     r.Header.Get(correlation.HeaderStoryID),
			ProjectID:   r.Header.Get(correlation.HeaderProjectID),
			WorkspaceID: r.Header.Get(correlation.HeaderWorkspaceID),
		}
		ctx := correlation.WithIDs(r.Context(), ids)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
