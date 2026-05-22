package document

import (
	"strings"
	"sync"
)

// Template substitution.
//
// Syntax: `{{name}}` renders to the resolver's value for `name`.
// `\{\{` is the literal-brace escape: rendering preserves a `{{`
// that should not be interpreted as a placeholder opener.
//
// We do not use text/template here: it brings the full Go template
// language (conditionals, ranges, methods, dot navigation) — orders of
// magnitude more surface than this substrate needs, and operator-edited
// documents would inherit syntax surprises (e.g. `{{.}}` parsing as a
// dot expression). A flat segment parser keeps the contract small.
//
// Name characters: [A-Za-z0-9_]. Anything else inside the braces
// (whitespace, dots, etc.) leaves the opener as literal output. An
// unclosed `{{` flushes as literal too — gracefully degrade rather
// than fail the call.

// Parsed is the pre-tokenised form of a template body. Construct with
// Parse and apply with Render.
type Parsed struct {
	segments []segment
	// names is the set of placeholders for fast iteration when
	// computing unresolved_vars — kept in source order for stability.
	names []string
}

type segment struct {
	literal     string
	placeholder string // when non-empty, this segment renders to resolver(placeholder)
}

// Resolver maps a placeholder name to its computed value. ok=false
// means the variable is unresolved; Render surfaces it on the
// unresolved-list instead of failing.
type Resolver interface {
	Lookup(name string) (string, bool)
}

// ResolverFunc is the canonical Resolver adaptor for function values.
type ResolverFunc func(name string) (string, bool)

// Lookup implements Resolver.
func (f ResolverFunc) Lookup(name string) (string, bool) { return f(name) }

// Parse scans body into segments. Cost is O(len(body)); the parsed
// form is safe to share across goroutines and intended to live in the
// per-version cache.
func Parse(body string) *Parsed {
	p := &Parsed{}
	seen := map[string]struct{}{}
	for {
		i := strings.Index(body, "{{")
		if i < 0 {
			if body != "" {
				p.segments = append(p.segments, segment{literal: unescapeBraces(body)})
			}
			return p
		}
		// `\{\{` is literally two `\{` sequences in the source;
		// unescapeBraces strips the escapes from the emitted literal
		// below, so the brace-pair stays out of the placeholder parser.
		// Pre-text becomes a literal segment (with brace-escape applied).
		if i > 0 {
			p.segments = append(p.segments, segment{literal: unescapeBraces(body[:i])})
		}
		rest := body[i+2:]
		close := strings.Index(rest, "}}")
		if close < 0 {
			// Unclosed `{{` — treat as literal and stop scanning.
			p.segments = append(p.segments, segment{literal: unescapeBraces(body[i:])})
			return p
		}
		name := rest[:close]
		if !isValidName(name) {
			// Not a name — render the opener as literal, continue from
			// just past the `{{`. This is graceful degradation.
			p.segments = append(p.segments, segment{literal: "{{"})
			body = rest
			continue
		}
		p.segments = append(p.segments, segment{placeholder: name})
		if _, ok := seen[name]; !ok {
			seen[name] = struct{}{}
			p.names = append(p.names, name)
		}
		body = rest[close+2:]
	}
}

// Render applies the resolver to every placeholder. unresolved is the
// (deduped, source-ordered) list of names the resolver did not answer
// for; the placeholder text is preserved in the output for those.
func (p *Parsed) Render(r Resolver) (rendered string, unresolved []string) {
	if p == nil || len(p.segments) == 0 {
		return "", nil
	}
	var b strings.Builder
	for _, s := range p.segments {
		if s.placeholder == "" {
			b.WriteString(s.literal)
			continue
		}
		v, ok := r.Lookup(s.placeholder)
		if ok {
			b.WriteString(v)
			continue
		}
		b.WriteString("{{")
		b.WriteString(s.placeholder)
		b.WriteString("}}")
	}
	rendered = b.String()
	// Compute unresolved by querying the resolver once per known name —
	// avoids scanning segments again to dedupe.
	for _, name := range p.names {
		if _, ok := r.Lookup(name); !ok {
			unresolved = append(unresolved, name)
		}
	}
	return rendered, unresolved
}

// Names returns the deduplicated, source-ordered placeholder names.
// Exposed for callers that want to compute resolution coverage without
// rendering the body.
func (p *Parsed) Names() []string {
	if p == nil {
		return nil
	}
	out := make([]string, len(p.names))
	copy(out, p.names)
	return out
}

// unescapeBraces turns `\{` into `{` and `\}` into `}` in a literal
// segment. The opener-side escape is what protects a `{{name}}` from
// being interpreted as a placeholder; we also strip the closer-side
// escapes so an operator who wrote `\{\{name\}\}` ends up with a clean
// literal `{{name}}` in the rendered output, not `{{name\}\}`.
func unescapeBraces(s string) string {
	if !strings.ContainsAny(s, `\`) {
		return s
	}
	s = strings.ReplaceAll(s, `\{`, `{`)
	s = strings.ReplaceAll(s, `\}`, `}`)
	return s
}

func isValidName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !(r == '_' ||
			(r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// Cache holds parsed templates keyed by (document_id, version). Per
// the substrate contract, a (doc, version) pair has an immutable body,
// so a cached *Parsed is safe to share for the binary's lifetime.
type Cache struct {
	m sync.Map // key: docID + ":" + strconv.Itoa(version), val: *Parsed
}

// Get returns a cached Parsed for (docID, version), parsing body and
// caching the result on a miss. Concurrent first-misses on the same
// key may parse more than once; the late writer wins. The Parsed
// values are deeply immutable so duplicate parses are equivalent.
func (c *Cache) Get(docID string, version int, body string) *Parsed {
	k := cacheKey(docID, version)
	if v, ok := c.m.Load(k); ok {
		return v.(*Parsed)
	}
	p := Parse(body)
	c.m.Store(k, p)
	return p
}

func cacheKey(docID string, version int) string {
	var b strings.Builder
	b.WriteString(docID)
	b.WriteByte(':')
	// inline int-to-string to avoid pulling strconv into the package's
	// import surface for a single sub-thousand version-id call site.
	if version == 0 {
		b.WriteByte('0')
	} else {
		neg := version < 0
		if neg {
			version = -version
		}
		buf := [20]byte{}
		i := len(buf)
		for version > 0 {
			i--
			buf[i] = byte('0' + version%10)
			version /= 10
		}
		if neg {
			i--
			buf[i] = '-'
		}
		b.Write(buf[i:])
	}
	return b.String()
}
