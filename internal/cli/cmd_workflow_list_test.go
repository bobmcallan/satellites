package cli

import "testing"

// mkWF builds a matSkill projecting a kind:workflow with the given applies_to.
func mkWF(name, source string, appliesTo ...string) matSkill {
	at := ""
	for i, a := range appliesTo {
		if i > 0 {
			at += ", "
		}
		at += "\"" + a + "\""
	}
	raw := "---\nname: " + name + "\nkind: workflow\napplies_to: [" + at + "]\ndescription: d-" + name + "\n---\n" +
		"# wf\n\n```yaml\nstates:\n  - backlog\n  - done\ntransitions:\n  - {from: backlog, to: done, reviewer_skill: \"g\"}\n```\n"
	return matSkill{name: name, kind: "workflow", description: "d-" + name, body: raw, raw: raw}
}

// TestPaletteRanking: specific applies_to ranks above wildcard; within a tier
// local > skill > embed; the top row is marked default and equals the engine's
// pick (sty_ed12634d AC2).
func TestPaletteRanking(t *testing.T) {
	local := []matSkill{
		mkWF("local-baseline", "local", "*"),           // wildcard, local
		mkWF("local-infra", "local", "infrastructure"), // specific, local
	}
	skills := []matSkill{mkWF("skill-infra", "skill", "infrastructure")} // specific, skill
	embed := []matSkill{
		mkWF("embed-parent", "embed", "parent"), // non-matching
		mkWF("embed-baseline", "embed", "*"),    // wildcard, embed
	}

	rows := paletteFor("infrastructure", local, skills, embed)

	// embed-parent does not cover infrastructure → excluded.
	for _, r := range rows {
		if r.Name == "embed-parent" {
			t.Fatalf("embed-parent (applies_to parent) must not appear for infrastructure")
		}
	}
	// Expected order: local-infra (specific,local), skill-infra (specific,skill),
	// local-baseline (wildcard,local), embed-baseline (wildcard,embed).
	want := []string{"local-infra", "skill-infra", "local-baseline", "embed-baseline"}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d (%v)", len(rows), len(want), rowNames(rows))
	}
	for i, w := range want {
		if rows[i].Name != w {
			t.Errorf("row %d = %q, want %q (order %v)", i, rows[i].Name, w, rowNames(rows))
		}
	}
	if !rows[0].Default {
		t.Error("top row must be marked default")
	}
	for _, r := range rows[1:] {
		if r.Default {
			t.Errorf("only the top row is default; %q also marked", r.Name)
		}
	}
	if !rows[0].Specific {
		t.Error("the specific match must rank first (Specific=true)")
	}
}

// TestPaletteWildcardOnly: a category covered only by wildcards still resolves a
// default (the first wildcard in source order), parity with ResolveGoverningWorkflow.
func TestPaletteWildcardOnly(t *testing.T) {
	local := []matSkill{mkWF("local-baseline", "local", "*")}
	embed := []matSkill{mkWF("embed-baseline", "embed", "*")}
	rows := paletteFor("feature", local, nil, embed)
	if len(rows) != 2 || rows[0].Name != "local-baseline" || !rows[0].Default {
		t.Fatalf("wildcard-only palette = %v, want local-baseline default first", rowNames(rows))
	}
	if rows[0].Specific {
		t.Error("a wildcard default must report Specific=false")
	}
}

// TestPaletteEmpty: a category no workflow covers yields an empty palette.
func TestPaletteEmpty(t *testing.T) {
	local := []matSkill{mkWF("local-parent", "local", "parent")}
	if rows := paletteFor("infrastructure", local, nil, nil); len(rows) != 0 {
		t.Fatalf("uncovered category should yield no rows, got %v", rowNames(rows))
	}
}

func rowNames(rows []workflowListRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Name
	}
	return out
}
