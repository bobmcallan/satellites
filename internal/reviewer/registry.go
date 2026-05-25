package reviewer

import (
	"context"
)

// Registry is the runtime collection of loaded reviewers + the
// client they all share. The orchestrator queries the registry on
// every story_create / story_update post-write; the registry
// dispatches against each enabled reviewer in turn.
//
// Concurrency: the registry is read-only after construction; the
// underlying map is therefore safe to read without a lock.
// Operators rebuild the binary to change reviewer state.
type Registry struct {
	Defs   map[string]Definition
	Client Client
}

// NewRegistry returns a registry bound to the given client. Pass
// a nil client to construct a registry that loads definitions but
// refuses to dispatch (the runner will return an explicit
// "client not configured" error per reviewer). Useful for boot
// diagnostics when ANTHROPIC_API_KEY is intentionally unset.
func NewRegistry(defs map[string]Definition, client Client) *Registry {
	if defs == nil {
		defs = map[string]Definition{}
	}
	return &Registry{Defs: defs, Client: client}
}

// EnabledNames returns the names of every enabled reviewer, in
// arbitrary order. The orchestrator uses it to decide whether
// there is any work to dispatch.
func (r *Registry) EnabledNames() []string {
	names := []string{}
	for n, d := range r.Defs {
		if d.Enabled {
			names = append(names, n)
		}
	}
	return names
}

// RunAll dispatches every enabled reviewer against the given
// story. Findings from all reviewers are returned grouped by
// reviewer name. Errors are returned per reviewer so one failing
// reviewer does not block the others.
func (r *Registry) RunAll(ctx context.Context, story any) (map[string][]Finding, map[string]error) {
	findings := map[string][]Finding{}
	errs := map[string]error{}
	if r == nil {
		return findings, errs
	}
	for name, def := range r.Defs {
		if !def.Enabled {
			continue
		}
		out, err := Run(ctx, def, r.Client, story)
		if err != nil {
			errs[name] = err
			continue
		}
		findings[name] = out
	}
	return findings, errs
}
