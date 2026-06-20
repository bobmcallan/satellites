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
