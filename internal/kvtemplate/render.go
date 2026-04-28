// Package kvtemplate renders {{KEY}} placeholders inside config text
// (contract bodies, workflow specs, agent prompts) by resolving each key
// against a Resolver. It backs the four-scope KV substitution surface
// from epic:kv-scopes (story_6593bb8c).
//
// Hard-fail discipline: callers that need fail-loud semantics on
// missing keys check Result.Unresolved and reject the load when it is
// non-empty. Render itself never silently substitutes empty strings —
// unresolved keys are preserved in the output so callers can spot them
// AND named in Result.Unresolved so callers can report them
// structurally (per pr_evidence).
package kvtemplate

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/bobmcallan/satellites/internal/ledger"
)

// placeholderPattern matches a `{{ key }}` placeholder. Keys may
// contain alphanumeric characters, underscore, dot, colon, or hyphen.
// Whitespace inside the braces is tolerated.
var placeholderPattern = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_:.\-]+)\s*\}\}`)

// Resolver looks up a value for a key. Found=false signals "no value
// at any tier in the caller's resolution chain"; Render then records
// the key in Result.Unresolved.
type Resolver interface {
	Resolve(ctx context.Context, key string) (value string, found bool, err error)
}

// Result is the output of Render. Text holds the (possibly partially)
// rendered string; Unresolved enumerates keys that the resolver did
// not find. Repeated occurrences of the same unresolved key are
// deduplicated.
type Result struct {
	Text       string
	Unresolved []string
}

// Render walks every {{KEY}} placeholder in text, calls resolver.Resolve
// per unique key, and substitutes the value. Resolver errors propagate.
// Unresolved keys are recorded in Result.Unresolved AND left in the
// output verbatim so callers that fail-loud can inspect both.
//
// The function is idempotent on already-resolved text: when no
// placeholders remain, Render returns the input unchanged with empty
// Unresolved.
func Render(ctx context.Context, text string, resolver Resolver) (Result, error) {
	matches := placeholderPattern.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return Result{Text: text}, nil
	}
	cache := make(map[string]string)
	missingSet := make(map[string]struct{})
	missingOrder := make([]string, 0)
	var resolveErr error
	rendered := placeholderPattern.ReplaceAllStringFunc(text, func(match string) string {
		if resolveErr != nil {
			return match
		}
		key := strings.TrimSpace(match[2 : len(match)-2])
		if v, ok := cache[key]; ok {
			return v
		}
		v, found, err := resolver.Resolve(ctx, key)
		if err != nil {
			resolveErr = fmt.Errorf("kvtemplate: resolve %q: %w", key, err)
			return match
		}
		if !found {
			if _, dup := missingSet[key]; !dup {
				missingSet[key] = struct{}{}
				missingOrder = append(missingOrder, key)
			}
			return match
		}
		cache[key] = v
		return v
	})
	if resolveErr != nil {
		return Result{}, resolveErr
	}
	return Result{Text: rendered, Unresolved: missingOrder}, nil
}

// LedgerResolver wraps a ledger.Store + KVResolveOptions so a single
// resolver can be passed into Render. Keys are resolved through the
// `system → user → project → workspace` chain (story_405b7221).
type LedgerResolver struct {
	Store       ledger.Store
	Opts        ledger.KVResolveOptions
	Memberships []string
}

// Resolve implements Resolver for LedgerResolver.
func (l LedgerResolver) Resolve(ctx context.Context, key string) (string, bool, error) {
	row, found, err := ledger.KVResolveScoped(ctx, l.Store, key, l.Opts, l.Memberships)
	if err != nil {
		return "", false, err
	}
	if !found {
		return "", false, nil
	}
	return row.Value, true, nil
}
