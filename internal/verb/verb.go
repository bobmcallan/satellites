// Package verb is V5's single execution path.
//
// Every substrate operation is registered here as a named function that
// takes JSON in and returns JSON out. Both the CLI (`satellites exec
// <verb>`) and the MCP transport (`internal/mcpserver`) dispatch to the
// same Invoke functions — there is exactly one implementation per verb.
//
// Adding a new verb is one new file: declare a *Verb, call Register
// from an init(). The CLI's generic exec subcommand and the MCP
// server's tool catalog both pick it up automatically.
package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

// Verb is a named entry in the dispatch registry.
type Verb struct {
	Name        string
	Description string
	Invoke      func(ctx context.Context, req json.RawMessage) (json.RawMessage, error)
}

var (
	mu    sync.RWMutex
	verbs = map[string]*Verb{}
)

// Register adds a verb to the global registry. Panics on duplicate
// names — verb names are a typed namespace and collisions are bugs.
func Register(v *Verb) {
	mu.Lock()
	defer mu.Unlock()
	if _, exists := verbs[v.Name]; exists {
		panic(fmt.Sprintf("verb: %q already registered", v.Name))
	}
	verbs[v.Name] = v
}

// Get returns the verb registered under name, or nil.
func Get(name string) *Verb {
	mu.RLock()
	defer mu.RUnlock()
	return verbs[name]
}

// Catalog returns the names of every registered verb, sorted.
func Catalog() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(verbs))
	for n := range verbs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Dispatch invokes a verb by name with raw JSON. Both CLI and MCP
// transports call here — same code path, same response shape.
func Dispatch(ctx context.Context, name string, req json.RawMessage) (json.RawMessage, error) {
	v := Get(name)
	if v == nil {
		return nil, fmt.Errorf("verb: unknown %q", name)
	}
	return v.Invoke(ctx, req)
}
