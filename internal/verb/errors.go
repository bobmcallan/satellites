package verb

import "errors"

// Sentinel errors verbs return to convey HTTP semantics to the exec
// transport. Wrap (or wrap+fmt.Errorf with %w) one of these so the
// server can map to the right status code; the MCP transport stringifies
// regardless. Anything not classified falls back to 400 bad-request.
var (
	ErrBadRequest   = errors.New("verb: bad request")
	ErrUnauthorized = errors.New("verb: unauthorized")
	ErrForbidden    = errors.New("verb: forbidden")
	ErrNotFound     = errors.New("verb: not found")
)
