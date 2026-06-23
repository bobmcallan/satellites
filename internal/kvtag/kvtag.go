// Package kvtag is the canonical helper for key:value ("KV") tags carried in a
// document's free-form []string tag set. Tags like `type:diagram` and
// `phase:discovery` are a single colon-separated key/value; this package reads,
// sets, and normalizes them so call sites stop re-implementing the split
// (the older ad-hoc readers tagValue/tagOrderValue/hasTagWithPrefix).
//
// Multi-valued keys stay multi-valued: a story may legitimately carry both
// `area:cli` and `area:bootstrap`. Only the keys named in SingleValued are
// collapsed last-wins by Normalize. That set is a small named CONVENTION the
// substrate enforces, not a baked process rule — it lists the keys a document
// holds at most once.
package kvtag

import "strings"

// SingleValued names the KV keys a document carries at most once. Normalize
// collapses duplicates of these keys to their last occurrence; every other key
// is left untouched. `type` is the classification, `phase` the lifecycle phase
// (discovery|build|…) — both are one-per-document by construction.
var SingleValued = []string{"type", "phase"}

// split returns the key and value of a `key:value` tag and whether it parsed.
// The split is on the FIRST colon, so a value may itself contain colons
// (`kind:mcp-startup` → key=kind, value=mcp-startup). A tag with no colon, a
// leading colon, or an empty value does not parse as KV.
func split(tag string) (key, value string, ok bool) {
	i := strings.IndexByte(tag, ':')
	if i <= 0 || i == len(tag)-1 {
		return "", "", false
	}
	return tag[:i], tag[i+1:], true
}

// Value returns the value of the first `key:value` tag for key, or "" when no
// KV tag with that key is present.
func Value(tags []string, key string) string {
	for _, t := range tags {
		if k, v, ok := split(t); ok && k == key {
			return v
		}
	}
	return ""
}

// Has reports whether any `key:value` tag with the given key is present.
func Has(tags []string, key string) bool {
	for _, t := range tags {
		if k, _, ok := split(t); ok && k == key {
			return true
		}
	}
	return false
}

// Set returns tags with every existing `key:*` tag removed and a single
// `key:value` appended — a replace-by-key that leaves a tag set holding at most
// one value for key. Non-matching tags keep their relative order; the new tag
// is appended last. The input slice is not mutated.
func Set(tags []string, key, value string) []string {
	out := make([]string, 0, len(tags)+1)
	for _, t := range tags {
		if k, _, ok := split(t); ok && k == key {
			continue
		}
		out = append(out, t)
	}
	return append(out, key+":"+value)
}

// Normalize collapses every SingleValued key to its last occurrence, dropping
// earlier duplicates so a stored document holds at most one `type:` and one
// `phase:` (last-wins). Multi-valued keys and non-KV tags pass through
// unchanged, in order. The input slice is not mutated; nil maps to nil.
func Normalize(tags []string) []string {
	if tags == nil {
		return nil
	}
	last := make(map[string]int, len(SingleValued))
	for i, t := range tags {
		if k, _, ok := split(t); ok && isSingleValued(k) {
			last[k] = i
		}
	}
	out := make([]string, 0, len(tags))
	for i, t := range tags {
		if k, _, ok := split(t); ok && isSingleValued(k) && last[k] != i {
			continue // an earlier duplicate of a single-valued key
		}
		out = append(out, t)
	}
	return out
}

func isSingleValued(key string) bool {
	for _, k := range SingleValued {
		if k == key {
			return true
		}
	}
	return false
}
