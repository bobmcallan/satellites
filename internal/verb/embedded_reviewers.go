// Embedded config/skills reviewer resolution (epic:system-substrate).
//
// A satellites system reviewer ships EMBEDDED in the client binary under
// config/skills and is resolved from that embed when no local .claude/skills
// override is present. There is no separate "internal gate" home and no
// embed-wins protection: per the operator's local-WINS model the resolver
// consults .claude/skills first, then this embed, then the server — so a repo
// may override any default reviewer in place. This is mechanism only — the
// lookup, not any process.

package verb

import (
	"io/fs"
	"sort"
	"strings"

	substrate "github.com/bobmcallan/satellites/config"
)

// configSkillRaw returns the raw bytes of an embedded config/skills reviewer
// (the client binary's authored-process reviewer home) and true when one is
// registered under that name. A config/skills reviewer is EDITABLE substrate —
// the resolver consults a local .claude/skills override BEFORE this embed. The
// file carries its frontmatter verbatim, the same shape the resolver parses.
func configSkillRaw(name string) ([]byte, bool) {
	raw, err := fs.ReadFile(substrate.FS, "skills/"+name+".md")
	if err != nil {
		return nil, false
	}
	return raw, true
}

// IsConfigSkill reports whether name resolves to an embedded config/skills
// reviewer shipped in the client binary. It lets the process validators
// (workflow check, story governance) treat a binary-resident reviewer as
// AVAILABLE rather than missing/unresolvable — the resolver runs it from the
// embed with no server.
func IsConfigSkill(name string) bool {
	_, ok := configSkillRaw(name)
	return ok
}

// ConfigSkillBody returns the embedded config/skills body for name, so
// `satellites skill get` can SURFACE a binary-embedded system reviewer instead of
// reporting "not found" (sty_4300e117) — the discoverability gap that let an agent
// conclude no completion gate existed and hand-stamp a task to complete.
func ConfigSkillBody(name string) (string, bool) {
	raw, ok := configSkillRaw(name)
	if !ok {
		return "", false
	}
	return string(raw), true
}

// ConfigSkillNames lists every embedded config/skills reviewer (sorted), so
// `satellites skill list` can include the binary-embedded system gates that the
// server-backed document_list never returns.
func ConfigSkillNames() []string {
	entries, err := fs.ReadDir(substrate.FS, "skills")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if n := strings.TrimSuffix(e.Name(), ".md"); n != e.Name() {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}
