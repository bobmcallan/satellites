// Satellites-internal gate definitions (epic:satellites-backbone 2.2).
//
// An internal gate is satellites' OWN governance — the gates of the
// upsert→review flow and satellites' own intent gates. They ship EMBEDDED in
// the binary (a protected location) and are injected into the `claude -p`
// gate context, NEVER materialised to the user-editable `.claude/skills/`. The
// dispatcher resolves an internal gate from here BEFORE the worktree, so a user
// who places or edits a `.claude/skills/<name>` of the same name cannot alter
// satellites' own governance. This is mechanism only — it ships no project's
// gate, just the protected home and the lookup.

package verb

import "embed"

// internalGatesFS holds the embedded internal gate definitions. Each file is a
// <name>.md SKILL with ordinary frontmatter (kind:reviewer, an optional
// functional check:) and a body — the same shape a materialised skill carries,
// minus the sync stamp.
//
//go:embed internalgates/*.md
var internalGatesFS embed.FS

// internalGateRaw returns the embedded SKILL bytes for a satellites-internal
// gate and true when one is registered under that name. The dispatcher calls
// this first; a hit means the gate is injected from the binary and the worktree
// copy (if any) is never consulted.
func internalGateRaw(name string) ([]byte, bool) {
	raw, err := internalGatesFS.ReadFile("internalgates/" + name + ".md")
	if err != nil {
		return nil, false
	}
	return raw, true
}

// IsInternalGate reports whether name resolves to a satellites-internal embedded
// gate. A workflow may NAME such a gate even though it is never materialised to
// .claude/skills, so the process-drift checks (workflow check, story governance)
// must treat it as AVAILABLE rather than missing/unresolvable
// (epic:satellites-backbone 2.4.1).
func IsInternalGate(name string) bool {
	_, ok := internalGateRaw(name)
	return ok
}
