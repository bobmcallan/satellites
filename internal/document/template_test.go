package document

import (
	"reflect"
	"testing"
)

func TestParseRender_BasicSubstitution(t *testing.T) {
	r := ResolverFunc(func(name string) (string, bool) {
		m := map[string]string{"version": "v0.0.42", "os": "linux", "arch": "amd64"}
		v, ok := m[name]
		return v, ok
	})
	got, unresolved := Parse("binary={{version}} platform={{os}}/{{arch}}").Render(r)
	if got != "binary=v0.0.42 platform=linux/amd64" {
		t.Fatalf("rendered: %q", got)
	}
	if len(unresolved) != 0 {
		t.Fatalf("unexpected unresolved: %+v", unresolved)
	}
}

func TestParseRender_UnresolvedPreservesPlaceholder(t *testing.T) {
	r := ResolverFunc(func(name string) (string, bool) { return "", false })
	got, unresolved := Parse("hello {{missing}} world").Render(r)
	if got != "hello {{missing}} world" {
		t.Fatalf("rendered: %q", got)
	}
	if !reflect.DeepEqual(unresolved, []string{"missing"}) {
		t.Fatalf("unresolved: %+v", unresolved)
	}
}

func TestParseRender_RepeatedPlaceholders(t *testing.T) {
	r := ResolverFunc(func(name string) (string, bool) {
		if name == "x" {
			return "X", true
		}
		return "", false
	})
	got, unresolved := Parse("{{x}}-{{x}}-{{y}}-{{x}}-{{y}}").Render(r)
	if got != "X-X-{{y}}-X-{{y}}" {
		t.Fatalf("rendered: %q", got)
	}
	// unresolved should be deduped + source-ordered.
	if !reflect.DeepEqual(unresolved, []string{"y"}) {
		t.Fatalf("unresolved: %+v", unresolved)
	}
}

func TestParseRender_EscapeLiteralBraces(t *testing.T) {
	r := ResolverFunc(func(name string) (string, bool) { return "X", true })
	got, _ := Parse(`literal \{\{name\}\} stays`).Render(r)
	// `\{\{name\}\}` should round-trip to a clean `{{name}}` literal —
	// the opener escape stops placeholder parsing, the closer escape
	// keeps the trailing braces clean in the output.
	if got != "literal {{name}} stays" {
		t.Fatalf("rendered: %q", got)
	}
}

func TestParseRender_InvalidName(t *testing.T) {
	// `{{ name }}` (with spaces) is not a valid name; treat as literal.
	got, _ := Parse(`hello {{ name }} world`).Render(ResolverFunc(func(string) (string, bool) { return "X", true }))
	if got != "hello {{ name }} world" {
		t.Fatalf("rendered: %q", got)
	}
}

func TestParseRender_UnclosedOpener(t *testing.T) {
	got, _ := Parse(`hello {{ world`).Render(ResolverFunc(func(string) (string, bool) { return "X", true }))
	if got != "hello {{ world" {
		t.Fatalf("rendered: %q", got)
	}
}

func TestCache_DedupesParses(t *testing.T) {
	var c Cache
	a := c.Get("doc_1", 1, "body {{x}}")
	b := c.Get("doc_1", 1, "body {{x}}")
	if a != b {
		t.Fatalf("expected cache hit to return same pointer")
	}
	c2 := c.Get("doc_1", 2, "body {{x}}")
	if a == c2 {
		t.Fatalf("expected different version to parse fresh")
	}
}

func TestNames_DedupedAndOrdered(t *testing.T) {
	p := Parse(`{{b}} {{a}} {{b}} {{c}} {{a}}`)
	got := p.Names()
	want := []string{"b", "a", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("names: got %v want %v", got, want)
	}
}
