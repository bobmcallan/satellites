package configseed

import (
	"errors"
	"testing"
)

func TestParse_HappyPath(t *testing.T) {
	t.Parallel()
	src := []byte("---\nname: foo\ntags: [a, b]\n---\n# Heading\n\nbody text\n")
	fm, body, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if fm.String("name") != "foo" {
		t.Errorf("name = %q, want foo", fm.String("name"))
	}
	tags := fm.StringSlice("tags")
	if len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Errorf("tags = %v, want [a b]", tags)
	}
	if string(body) != "# Heading\n\nbody text\n" {
		t.Errorf("body = %q", string(body))
	}
}

func TestParse_NoFrontmatter(t *testing.T) {
	t.Parallel()
	_, _, err := Parse([]byte("# Just markdown\n"))
	if !errors.Is(err, ErrNoFrontmatter) {
		t.Errorf("want ErrNoFrontmatter, got %v", err)
	}
}

func TestParse_UnterminatedFrontmatter(t *testing.T) {
	t.Parallel()
	_, _, err := Parse([]byte("---\nname: foo\n# never closed\n"))
	if err == nil {
		t.Errorf("expected error for unterminated frontmatter")
	}
}

func TestParse_StringSlice_AcceptsScalar(t *testing.T) {
	t.Parallel()
	fm := Frontmatter{"perms": "Read:**"}
	got := fm.StringSlice("perms")
	if len(got) != 1 || got[0] != "Read:**" {
		t.Errorf("got %v, want [Read:**]", got)
	}
}
