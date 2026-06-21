package main

import (
	"strings"
	"testing"
)

// TestPlanSeedFileSkipsMalformed proves the boot-seed's resilience contract
// (sty_adad79f8): a malformed embed file is SKIPPED (not fatal) and a
// well-formed file alongside it still seeds. planSeedFile is the pure decision
// the boot loop runs per file.
func TestPlanSeedFileSkipsMalformed(t *testing.T) {
	always := func(string, string, []string) bool { return true }

	// Malformed frontmatter — an unquoted ": " in the description value, the
	// exact shape that crash-looped pprod (sty_50458f17).
	bad := []byte("---\nname: x\ntype: skill\ndescription: do X: then Y\n---\nbody\n")
	p := planSeedFile("skills/x.md", bad, always)
	if !p.Skip {
		t.Fatalf("malformed file must be skipped, got %+v", p)
	}
	if !strings.Contains(p.Reason, "parse") {
		t.Errorf("skip reason should name the parse failure, got %q", p.Reason)
	}

	// Well-formed system skill — seeded.
	good := []byte("---\nname: good-reviewer\ntype: skill\ndescription: a clean description\n---\nbody\n")
	g := planSeedFile("skills/good-reviewer.md", good, always)
	if g.Skip || g.ClientHomed {
		t.Fatalf("well-formed system file must seed, got %+v", g)
	}
	if g.Name != "good-reviewer" || g.Type != "skill" {
		t.Errorf("resolved seed wrong: %+v", g)
	}

	// A malformed file alongside a good one: the good one STILL seeds — the boot
	// loop log-and-skips the bad and continues the rest.
	files := map[string][]byte{"bad.md": bad, "good.md": good}
	seeded, skips := 0, 0
	for fn, raw := range files {
		switch pp := planSeedFile(fn, raw, always); {
		case pp.Skip:
			skips++
		case !pp.ClientHomed:
			seeded++
		}
	}
	if seeded != 1 || skips != 1 {
		t.Fatalf("expected 1 seeded + 1 skipped, got seeded=%d skipped=%d", seeded, skips)
	}
}

// TestPlanSeedFileMisscoped: a non-system scope is skipped, not fatal.
func TestPlanSeedFileMisscoped(t *testing.T) {
	always := func(string, string, []string) bool { return true }
	raw := []byte("---\nname: y\ntype: skill\nscope: project\ndescription: x\n---\nbody\n")
	p := planSeedFile("skills/y.md", raw, always)
	if !p.Skip || !strings.Contains(p.Reason, "scope") {
		t.Fatalf("non-system scope must be skipped with a scope reason, got %+v", p)
	}
}

// TestPlanSeedFileClientHomed: a well-formed but client-homed file is neither
// seeded nor counted as a (malformed) skip.
func TestPlanSeedFileClientHomed(t *testing.T) {
	never := func(string, string, []string) bool { return false }
	raw := []byte("---\nname: client-skill\ntype: skill\ndescription: x\n---\nbody\n")
	p := planSeedFile("skills/client-skill.md", raw, never)
	if !p.ClientHomed || p.Skip {
		t.Fatalf("client-homed file should be ClientHomed (not skip/seed), got %+v", p)
	}
}
