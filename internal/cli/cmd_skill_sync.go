// `satellites skill sync` — the pull half of the source round-trip
// (sty_52e10340). Where `skill upload` pushes config/ sources → substrate,
// `sync` pulls substrate type:skill rows → .claude/skills/<name>/SKILL.md so
// the gate runs the materialised install, and editing a rubric in the
// substrate takes effect after a sync with no hand-edit and no binary
// release.
//
// Ownership is keyed by an injected identity stamp, NOT the skill name
// prefix (sty_4b517016 built the stamp; operator decision on sty_52e10340).
// sync updates or removes a local skill it materialised (one carrying the
// stamp). An unstamped local copy that shares a substrate name is adopted when
// byte-identical (stamped in place) or surfaced as a conflict when it differs
// (sty_ad9e9b4b) — never silently left stale; only an unstamped local with NO
// substrate row is genuinely operator-authored and left untouched. Pull-only:
// it never writes back to the substrate.
//
// Composes the existing document_list / document_get verbs — no new MCP verb
// (no-new-mcp-verbs).

package cli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/bobmcallan/satellites/internal/frontmatter"
	"github.com/spf13/cobra"
)

// Conflict-resolution policy for an edited (stamped, hash-mismatch) local
// skill. `prompt` (the default) asks the operator interactively; it degrades
// to `local` when stdin is not a terminal so automation never blocks or
// silently clobbers a local edit.
const (
	conflictPrompt = "prompt"
	conflictLocal  = "local"
	conflictForce  = "force"
)

// stampMarker delimits the injected sync-identity block in a materialised
// SKILL.md. The block sits on the line immediately after the frontmatter
// close — frontmatter stays first so Claude Code reads the skill's real
// description (sty_98bf2818); a body with no frontmatter falls back to
// stamp-first. Either way the stamp is excluded from the content hash, so
// the hash reflects only what the substrate stores (sty_4b517016 contract).
// The legacy stamp-at-byte-0 layout is still recognised on read and
// migrated on the next sync.
const (
	stampBegin = "<!-- satellites-sync:begin"
	stampEnd   = "satellites-sync:end -->"
)

// skillStamp is the identity a materialised skill carries so a later sync
// can reconcile it: which substrate row it came from, at what version, and
// the hash of the authored body it was cut from.
type skillStamp struct {
	DocumentID string `json:"document_id"`
	Version    int    `json:"version"`
	Hash       string `json:"hash"`
}

// substrateSkill is one row sync pulls: its identity + the authored body
// (a complete SKILL.md, frontmatter intact — sty_4b517016 round-trip).
type substrateSkill struct {
	Name       string
	DocumentID string
	Version    int
	Body       string
}

// syncAction is the reconcile verdict for one name.
type syncAction int

const (
	actionInstall        syncAction = iota // substrate row, no local copy
	actionUpdate                           // stamped local copy, substrate version advanced, unedited
	actionSkip                             // stamped local copy already current
	actionRemove                           // stamped local copy, substrate row gone
	actionConflict                         // edited stamped copy, OR unstamped copy of a substrate skill that differs — resolve, don't silently overwrite
	actionLeaveUnstamped                   // unstamped local with NO substrate row — genuinely operator-authored, never touch
	actionMatchUnstamped                   // unstamped local byte-identical to the substrate body — adopt it (stamp in place)
	actionMigrate                          // stamped local copy, current + unedited, but legacy stamp-first layout — rewrite in the frontmatter-first layout
)

func (a syncAction) String() string {
	switch a {
	case actionInstall:
		return "install"
	case actionUpdate:
		return "update"
	case actionSkip:
		return "current"
	case actionRemove:
		return "remove"
	case actionConflict:
		return "conflict"
	case actionLeaveUnstamped:
		return "leave"
	case actionMatchUnstamped:
		return "adopt"
	case actionMigrate:
		return "migrate"
	}
	return "?"
}

// reason is a one-line explanation printed beside each action so the verdict
// is self-explanatory — in particular so `current` (you ARE up to date) is not
// confused with `leave` (unmanaged; you are NOT getting substrate updates).
func (a syncAction) reason() string {
	switch a {
	case actionInstall:
		return "new — materialised from substrate"
	case actionUpdate:
		return "substrate advanced — updated"
	case actionSkip:
		return "already up to date"
	case actionRemove:
		return "substrate row gone — removed"
	case actionConflict:
		return "local differs from substrate — resolve with --on-conflict"
	case actionLeaveUnstamped:
		return "operator-authored (no substrate row) — not managed"
	case actionMatchUnstamped:
		return "unstamped local matched substrate — stamp adopted"
	case actionMigrate:
		return "legacy stamp-first layout — rewritten frontmatter-first"
	}
	return ""
}

// localSkill is what sync reads off disk for one materialised name: whether
// it has a stamp, and the hash of its current authored body (stamp block
// excluded) so an operator edit can be detected.
type localSkill struct {
	Name      string
	Stamped   bool
	Stamp     skillStamp
	BodyHash  string // hash of on-disk authored body (stamp block stripped)
	Malformed bool   // stamp block present but unparseable
	Legacy    bool   // stamped in the legacy stamp-first layout — migrate on sync
	Scope     string // frontmatter scope of the authored body ("" when project/unset)
}

// localSkillName is the name a substrate skill is materialised under in
// .claude/skills/. Every substrate skill carries the `satellites-` prefix
// locally — the human-visible owner marker (see satellites-skill-naming) —
// even when its source/row name omits it, so the source prefix is optional and
// the local tree is unambiguously substrate-owned. Idempotent: an
// already-prefixed name is returned unchanged. The reconcile still keys on the
// injected stamp; this only governs the on-disk directory name.
func localSkillName(rowName string) string {
	if strings.HasPrefix(rowName, "satellites-") {
		return rowName
	}
	return "satellites-" + rowName
}

// hashBody computes the content hash the stamp records — over the authored
// body with any injected stamp block removed, so re-materialising the same
// substrate body is a no-op.
func hashBody(authored string) string {
	sum := sha256.Sum256([]byte(authored))
	return hex.EncodeToString(sum[:])
}

// stampBlock renders the injected identity block for an authored body.
func stampBlock(s skillStamp) string {
	b, _ := json.Marshal(s)
	return fmt.Sprintf("%s %s %s\n", stampBegin, string(b), strings.TrimPrefix(stampEnd, ""))
}

// splitAfterFrontmatter splits raw at the end of its YAML frontmatter
// block: head is the frontmatter including the closing `---` line (with
// its newline), body is everything after. found=false (head empty, body
// = raw) when raw does not open with a frontmatter block.
func splitAfterFrontmatter(raw string) (head, body string, found bool) {
	if !strings.HasPrefix(raw, "---\n") && !strings.HasPrefix(raw, "---\r\n") {
		return "", raw, false
	}
	offset := strings.IndexByte(raw, '\n') + 1
	rest := raw[offset:]
	for len(rest) > 0 {
		lineEnd := strings.IndexByte(rest, '\n')
		line := rest
		if lineEnd >= 0 {
			line = rest[:lineEnd]
		}
		if strings.TrimRight(line, "\r") == "---" {
			if lineEnd < 0 {
				return raw, "", true
			}
			return raw[:offset+lineEnd+1], rest[lineEnd+1:], true
		}
		if lineEnd < 0 {
			break
		}
		offset += lineEnd + 1
		rest = rest[lineEnd+1:]
	}
	return "", raw, false
}

// cutStamp parses a stamp block at the START of s, returning the parsed
// stamp and the remainder (one following newline consumed). Caller
// guarantees s begins with stampBegin. Unparseable JSON or a missing end
// marker reports malformed.
func cutStamp(s string) (stamp skillStamp, rest string, ok, malformed bool) {
	end := strings.Index(s, stampEnd)
	if end < 0 {
		return skillStamp{}, s, false, true
	}
	jsonPart := strings.TrimSpace(s[len(stampBegin):end])
	rest = strings.TrimPrefix(s[end+len(stampEnd):], "\n")
	var st skillStamp
	if err := json.Unmarshal([]byte(jsonPart), &st); err != nil || st.DocumentID == "" {
		return skillStamp{}, rest, false, true
	}
	return st, rest, true, false
}

// splitStamp separates a materialised SKILL.md into its injected stamp (if
// present + parseable) and the authored body around it. Two positions are
// recognised: the line immediately after the frontmatter close (current
// layout — frontmatter first so Claude Code reads the skill's description,
// sty_98bf2818) and the very first byte (legacy layout, reported via
// legacy so the reconcile migrates the file). A stamp string anywhere else
// — e.g. quoted in a code fence — is NOT a stamp: position-anchored, no
// false adoption. A file with no stamp returns ok=false and the whole
// content as the authored body.
func splitStamp(raw string) (stamp skillStamp, authored string, ok, malformed, legacy bool) {
	if strings.HasPrefix(raw, stampBegin) {
		st, rest, sok, smal := cutStamp(raw)
		return st, rest, sok, smal, sok
	}
	head, body, found := splitAfterFrontmatter(raw)
	if !found || !strings.HasPrefix(body, stampBegin) {
		return skillStamp{}, raw, false, false, false
	}
	st, rest, sok, smal := cutStamp(body)
	return st, head + rest, sok, smal, false
}

// materialise renders the on-disk SKILL.md content for a substrate skill:
// the authored frontmatter first, the injected stamp block on the line
// after its close, then the body — so Claude Code parses the frontmatter
// (description, dispatch fields) while sync keeps its identity marker. A
// body with no frontmatter degrades to stamp-first.
func materialise(s substrateSkill) string {
	stamp := skillStamp{DocumentID: s.DocumentID, Version: s.Version, Hash: hashBody(s.Body)}
	head, body, found := splitAfterFrontmatter(s.Body)
	if !found {
		return stampBlock(stamp) + s.Body
	}
	return head + stampBlock(stamp) + body
}

// reconcileAction decides what to do for one name given the substrate row
// (nil if absent) and the local copy (nil if absent). This is the pure core
// the tests pin (AC2); the command applies the verdict.
func reconcileAction(sub *substrateSkill, local *localSkill) syncAction {
	switch {
	case sub != nil && local == nil:
		return actionInstall
	case sub != nil && local != nil:
		if !local.Stamped {
			// Unstamped local of a substrate-owned skill. Byte-identical → adopt
			// it (stamp it) so it becomes managed. DIFFERS → it is stale-or-edited
			// and would otherwise silently never receive substrate updates; route
			// it to conflict so it is visible and resolvable (default keeps local,
			// --on-conflict force overwrites with a backup). A genuinely
			// operator-authored skill has NO substrate row and is handled in the
			// sub==nil branch below (→ leave).
			if local.BodyHash == hashBody(sub.Body) {
				return actionMatchUnstamped
			}
			return actionConflict
		}
		if local.Malformed {
			return actionConflict
		}
		if local.BodyHash != local.Stamp.Hash {
			// Operator edited a materialised copy since it was stamped.
			return actionConflict
		}
		if sub.DocumentID != "" && local.Stamp.DocumentID != "" && sub.DocumentID != local.Stamp.DocumentID {
			// The name was re-homed to a different substrate row (e.g. a scope
			// move re-seeds it under a new document id with a restarted version
			// counter) — a version compare across different rows is meaningless;
			// the unedited local copy must follow the new row.
			return actionUpdate
		}
		if sub.Version > local.Stamp.Version {
			return actionUpdate
		}
		if local.Legacy {
			// Current + unedited, but in the legacy stamp-first layout that
			// hides the frontmatter from Claude Code — rewrite in place.
			return actionMigrate
		}
		return actionSkip
	case sub == nil && local != nil:
		if !local.Stamped {
			return actionLeaveUnstamped
		}
		if local.Malformed || local.BodyHash != local.Stamp.Hash {
			return actionConflict
		}
		return actionRemove
	}
	return actionSkip
}

// readLocalSkill loads .claude/skills/<name>/SKILL.md and classifies it.
// Returns nil when no local copy exists.
func readLocalSkill(skillsRoot, name string) (*localSkill, error) {
	path := filepath.Join(skillsRoot, name, "SKILL.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	stamp, authored, ok, malformed, legacy := splitStamp(string(raw))
	// Hash the authored body whether or not it carries a stamp: a stamped
	// file's hash detects operator edits; an unstamped file's hash lets the
	// reconcile spot one that is byte-identical to the substrate (match).
	ls := &localSkill{Name: name, Stamped: ok, Stamp: stamp, Malformed: malformed, Legacy: legacy, BodyHash: hashBody(authored)}
	// Record the materialised skill's declared scope so the upload orphan
	// check can tell a project-owned skill (which needs a .satellites/skills/
	// source) from a system/workspace skill that sync legitimately
	// materialised here with no project source. A parse failure leaves Scope
	// empty — treated as project-scoped, the fail-closed default.
	if fm, _, ferr := frontmatter.Parse([]byte(authored)); ferr == nil {
		ls.Scope = strings.TrimSpace(fm.Scope)
	}
	return ls, nil
}

// reconcileSkills computes the per-name verdict across the union of the
// substrate set and the locally-materialised set. Local names are limited to
// those that carry a stamp OR appear in the substrate — an unstamped local
// skill not in the substrate is operator-authored and simply omitted (sync
// has no business with it). Deterministic order for stable output + tests.
//
// protected guards removal across scopes (sty_cae81f8c): when sync reconciles
// only one scope's substrate (an explicit --scope, or deploy's project scope)
// against the whole local tree, a stamped local owned by ANOTHER scope would
// otherwise read as an orphan and be removed. protected holds every local name
// present in ANY resolvable scope's substrate, so a `remove` verdict for a name
// still owned elsewhere is downgraded to `skip` — only a name absent from every
// scope is a true orphan. A nil/empty protected set leaves removal unguarded
// (single-scope behaviour, as before).
func reconcileSkills(subs []substrateSkill, locals []localSkill, protected map[string]bool) []syncPlanItem {
	subByName := map[string]substrateSkill{}
	for _, s := range subs {
		// Key on the materialised (prefixed) name so a substrate row pairs
		// with its local copy under .claude/skills/satellites-<name>/.
		subByName[localSkillName(s.Name)] = s
	}
	localByName := map[string]localSkill{}
	for _, l := range locals {
		localByName[l.Name] = l
	}
	names := map[string]bool{}
	for n := range subByName {
		names[n] = true
	}
	for n, l := range localByName {
		if l.Stamped { // unstamped + not-in-substrate is none of sync's business
			names[n] = true
		}
	}
	var ordered []string
	for n := range names {
		ordered = append(ordered, n)
	}
	sort.Strings(ordered)

	var plan []syncPlanItem
	for _, n := range ordered {
		var subPtr *substrateSkill
		if s, ok := subByName[n]; ok {
			sCopy := s
			subPtr = &sCopy
		}
		var localPtr *localSkill
		if l, ok := localByName[n]; ok {
			lCopy := l
			localPtr = &lCopy
		}
		action := reconcileAction(subPtr, localPtr)
		if action == actionRemove && protected[n] {
			// Owned by another scope (still in the union) — not a true orphan.
			action = actionSkip
		}
		plan = append(plan, syncPlanItem{Name: n, Action: action, Sub: subPtr})
	}
	return plan
}

// mergeByPrecedence folds several substrate skill sets into one keyed on the
// materialised (satellites-prefixed) name, with LATER sets winning a name
// collision. Callers pass sets low→high precedence — system, then workspace,
// then project — so the most-specific scope's body lands on disk
// (most-specific-wins, sty_cae81f8c). User-scope overrides are deliberately
// excluded: sync reconciles the shared rows, and a user override is applied at
// the dynamic-skill-index / gate layer, not on disk (sty_cbeeb452).
func mergeByPrecedence(sets ...[]substrateSkill) []substrateSkill {
	byName := map[string]substrateSkill{}
	for _, set := range sets {
		for _, s := range set {
			byName[localSkillName(s.Name)] = s
		}
	}
	out := make([]substrateSkill, 0, len(byName))
	for _, s := range byName {
		out = append(out, s)
	}
	return out
}

// syncPlanItem is one reconcile verdict the command applies.
type syncPlanItem struct {
	Name   string
	Action syncAction
	Sub    *substrateSkill // non-nil for install/update
}

func newSkillSyncCmd(configArg, userArg *string) *cobra.Command {
	var (
		scopeArg   string
		wsArg      string
		pjArg      string
		dryRun     bool
		root       string
		onConflict string
	)
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Materialise substrate skills into .claude/skills/ (pull-only, stamp-reconciled)",
		Long: `sync pulls type:skill rows from the substrate into
.claude/skills/<name>/SKILL.md so the gate runs the materialised install.

Pull-only and stamp-keyed: sync updates or removes a local skill it
materialised (one carrying the injected identity stamp). An unstamped local
copy of a substrate skill is adopted when byte-identical (stamped in place) or
reported as a conflict when it differs; only a skill with no substrate row at
all is treated as operator-authored and left untouched. Editing a rubric in the
substrate and re-running sync takes effect with no hand-edit and no binary
release.

Each line prints the action plus a one-line reason, so 'current' (already up to
date) is distinct from 'leave' (unmanaged, NOT receiving substrate updates).

A 'conflict' (a stamped local copy you have edited since it was materialised)
is resolved per --on-conflict: 'prompt' (default) asks you to force-overwrite
(backing up your copy to SKILL.md-<datetime-ms>.bak first) or leave the local
copy; 'local' always keeps your edit; 'force' always overwrites with a backup.
Outside a terminal, 'prompt' degrades to 'local' so nothing is clobbered.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			switch onConflict {
			case conflictPrompt, conflictLocal, conflictForce:
			default:
				return fmt.Errorf("--on-conflict must be one of prompt|local|force, got %q", onConflict)
			}
			return syncSkills(ctx, cmd.OutOrStdout(), cmd.InOrStdin(), scopeArg, wsArg, pjArg, *configArg, *userArg, root, onConflict, dryRun)
		},
	}
	cmd.Flags().StringVar(&scopeArg, "scope", "", "Scope to install/update (system / workspace / project); empty = all (the union). Removal is union-guarded regardless.")
	cmd.Flags().StringVar(&wsArg, "workspace", "", "workspace_id (required for scope=workspace or project)")
	cmd.Flags().StringVar(&pjArg, "project", "", "project_id (required for scope=project)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print the reconcile plan without writing files")
	cmd.Flags().StringVar(&root, "skills-root", "", "Materialisation root (default .claude/skills)")
	cmd.Flags().StringVar(&onConflict, "on-conflict", conflictPrompt, "How to resolve an edited local skill: prompt | local | force")
	return cmd
}

// resolveSkillsRoot decides where substrate skills materialise. Precedence:
//
//  1. an explicit --skills-root flag (used verbatim),
//  2. the config's skills_root (relative → resolved against the repo root),
//  3. the default <repo>/.claude/skills.
//
// The repo root is the directory that HOLDS .satellites/ (derived from the
// resolved satellites.toml path), NOT the process CWD — so a `deploy` run
// from inside .satellites/ no longer lands skills in .satellites/.claude/skills
// (sty_be65b4dd path bug). With no config on disk it falls back to a
// CWD-relative .claude/skills, preserving the unconfigured-repo behaviour.
func resolveSkillsRoot(flagRoot, configArg string) (string, error) {
	if strings.TrimSpace(flagRoot) != "" {
		return flagRoot, nil
	}
	cfg, path, err := cliconfig.Load(configArg)
	if err != nil && !errors.Is(err, cliconfig.ErrNotFound) {
		return "", err
	}
	// Repo root = the dir holding .satellites/ (one computation, shared with
	// log_path resolution via cliconfig.RepoRootFromConfigPath).
	repoRoot := cliconfig.RepoRootFromConfigPath(path)
	if s := strings.TrimSpace(cfg.SkillsRoot); s != "" {
		if filepath.IsAbs(s) {
			return s, nil
		}
		return filepath.Join(repoRoot, s), nil
	}
	return filepath.Join(repoRoot, ".claude", "skills"), nil
}

// syncSkills is the testable, reusable pull-half: list the substrate
// skills for a scope, reconcile them against the materialised local
// `.claude/skills/` tree by identity stamp, and apply (unless dryRun).
// Single source shared by `skill sync` and `satellites deploy`.
func syncSkills(ctx context.Context, out io.Writer, in io.Reader, scope, ws, pj, configArg, userArg, root, onConflict string, dryRun bool) error {
	skillsRoot, err := resolveSkillsRoot(root, configArg)
	if err != nil {
		return err
	}

	switch scope {
	case "", "system", "workspace", "project":
	default:
		return fmt.Errorf("--scope must be one of system|workspace|project (or empty for all), got %q", scope)
	}

	dispatch := func(ctx context.Context, name string, req json.RawMessage) (json.RawMessage, error) {
		return dispatchVerb(ctx, name, req, configArg, userArg)
	}

	// Resolve the scopes to reconcile. System is always pulled. Workspace and
	// project are pulled when a workspace_id / project_id resolves — passed in
	// (deploy) or read best-effort from .satellites/satellites.toml the way
	// deploy does. An unconfigured repo tolerates the miss and yields system
	// only (sty_cae81f8c). reconcile shared rows, not per-caller overrides.
	if strings.TrimSpace(pj) == "" {
		if rws, rpj, rerr := resolveDeployScope(ctx, configArg, userArg); rerr == nil {
			ws, pj = rws, rpj
		}
	}
	// project scope needs a project binding; a bare --scope project with no
	// config is the one case we surface rather than silently no-op.
	if scope == "project" && strings.TrimSpace(pj) == "" {
		return fmt.Errorf("skill sync: project scope needs a project_id — pass --project or set project_id in .satellites/satellites.toml")
	}

	sysSet, err := listSubstrateSkills(ctx, dispatch, "system", "", "", false)
	if err != nil {
		return err
	}
	var wsSet, pjSet []substrateSkill
	if strings.TrimSpace(ws) != "" {
		if wsSet, err = listSubstrateSkills(ctx, dispatch, "workspace", ws, "", false); err != nil {
			return err
		}
	}
	if strings.TrimSpace(pj) != "" {
		if pjSet, err = listSubstrateSkills(ctx, dispatch, "project", ws, pj, false); err != nil {
			return err
		}
	}

	// The union (precedence project > workspace > system) is what every
	// resolvable scope owns — its local names guard removal so no scope orphans
	// another's stamped skill. The install/update set is the union by default,
	// or a single scope when --scope narrows it.
	union := mergeByPrecedence(sysSet, wsSet, pjSet)
	protected := make(map[string]bool, len(union))
	for _, s := range union {
		protected[localSkillName(s.Name)] = true
	}

	var subs []substrateSkill
	switch scope {
	case "system":
		subs = sysSet
	case "workspace":
		subs = wsSet
	case "project":
		subs = pjSet
	default: // "" → all scopes
		subs = union
	}

	// Read the local copy for every substrate name plus every
	// already-materialised (stamped) local skill, so removals are seen.
	localByName := map[string]localSkill{}
	for _, s := range subs {
		ln := localSkillName(s.Name)
		if l, err := readLocalSkill(skillsRoot, ln); err != nil {
			return fmt.Errorf("read local skill %q: %w", ln, err)
		} else if l != nil {
			localByName[ln] = *l
		}
	}
	stampedLocal, err := readStampedLocalSkills(skillsRoot)
	if err != nil {
		return fmt.Errorf("scan local skills: %w", err)
	}
	for _, l := range stampedLocal {
		if _, seen := localByName[l.Name]; !seen {
			localByName[l.Name] = l
		}
	}
	locals := make([]localSkill, 0, len(localByName))
	for _, l := range localByName {
		locals = append(locals, l)
	}

	plan := reconcileSkills(subs, locals, protected)
	for _, item := range plan {
		if dryRun {
			fmt.Fprintf(out, "[dry-run] %-9s %s  (%s)\n", item.Action, item.Name, item.Action.reason())
			continue
		}
		// A conflict (edited stamped copy) is not applied blindly — it is
		// resolved per the operator's policy, which may back up + overwrite.
		if item.Action == actionConflict {
			if err := resolveConflict(out, in, onConflict, skillsRoot, item); err != nil {
				return err
			}
			continue
		}
		if err := applySyncItem(skillsRoot, item); err != nil {
			return err
		}
		fmt.Fprintf(out, "%-9s %s  (%s)\n", item.Action, item.Name, item.Action.reason())
	}
	return nil
}

// isInteractive reports whether in is an interactive terminal — used so the
// conflict prompt only fires for a human and degrades to "leave local" under
// automation. Dependency-free: a char-device stdin is a TTY.
func isInteractive(in io.Reader) bool {
	f, ok := in.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// resolveConflict applies the conflict policy for one edited (stamped,
// hash-mismatch) local skill. `prompt` asks the operator when interactive and
// otherwise leaves the local copy; `force` backs up + overwrites; `local`
// keeps the edit. A backup is written to <name>/SKILL.md-<datetime-ms>.bak
// before any overwrite, so the operator's edit is never lost.
func resolveConflict(out io.Writer, in io.Reader, policy, skillsRoot string, item syncPlanItem) error {
	choice := policy
	if policy == "" || policy == conflictPrompt {
		if isInteractive(in) {
			choice = promptConflict(out, in, item.Name)
		} else {
			fmt.Fprintf(out, "conflict %s — kept local (non-interactive; re-run with --on-conflict force to overwrite)\n", item.Name)
			return nil
		}
	}
	if choice == conflictForce {
		if item.Sub == nil {
			// Edited copy whose substrate row is gone — nothing to restore from.
			fmt.Fprintf(out, "conflict %s — kept local (no substrate row to restore from)\n", item.Name)
			return nil
		}
		bak, err := backupSkill(skillsRoot, item.Name)
		if err != nil {
			return fmt.Errorf("backup %s: %w", item.Name, err)
		}
		if err := applySyncItem(skillsRoot, syncPlanItem{Name: item.Name, Action: actionUpdate, Sub: item.Sub}); err != nil {
			return err
		}
		fmt.Fprintf(out, "force    %s (backed up your copy to %s)\n", item.Name, filepath.Base(bak))
		return nil
	}
	fmt.Fprintf(out, "local    %s (kept your edit)\n", item.Name)
	return nil
}

// promptConflict asks the operator how to resolve one conflict, returning the
// chosen policy. Anything but an explicit force defaults to leaving the local
// copy — the safe choice.
func promptConflict(out io.Writer, in io.Reader, name string) string {
	fmt.Fprintf(out, "conflict %s — your local edit differs from the substrate.\n  [f]orce overwrite (backs up your copy first)  ·  [l]eave local (default): ", name)
	sc := bufio.NewScanner(in)
	if sc.Scan() {
		switch strings.ToLower(strings.TrimSpace(sc.Text())) {
		case "f", "force":
			return conflictForce
		}
	}
	return conflictLocal
}

// backupSkill copies the current SKILL.md aside to
// <skillsRoot>/<name>/SKILL.md-<YYYYMMDD-HHMMSS-mmm>.bak before an overwrite
// and returns the backup path. The .bak sits beside SKILL.md and is ignored by
// the reconcile (which only reads SKILL.md).
func backupSkill(skillsRoot, name string) (string, error) {
	src := filepath.Join(skillsRoot, name, "SKILL.md")
	raw, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	// e.g. 20260605-091530-123
	ts := strings.Replace(time.Now().Format("20060102-150405.000"), ".", "-", 1)
	bak := filepath.Join(skillsRoot, name, "SKILL.md-"+ts+".bak")
	if err := os.WriteFile(bak, raw, 0o644); err != nil {
		return "", err
	}
	return bak, nil
}

// applySyncItem performs the verdict for one plan item. install/update and
// adopt (match) write the materialised, stamped SKILL.md; remove deletes it.
// Skips, conflicts, and leaves are reported but make no change.
func applySyncItem(skillsRoot string, item syncPlanItem) error {
	switch item.Action {
	case actionInstall, actionUpdate, actionMatchUnstamped, actionMigrate:
		// install/update materialise the substrate copy; adopt (match) writes the
		// SAME body plus the identity stamp so an unstamped-but-identical local
		// becomes managed (no content change); migrate rewrites a legacy
		// stamp-first file in the frontmatter-first layout (same content).
		dir := filepath.Join(skillsRoot, item.Name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(materialise(*item.Sub)), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", item.Name, err)
		}
	case actionRemove:
		if err := os.RemoveAll(filepath.Join(skillsRoot, item.Name)); err != nil {
			return fmt.Errorf("remove %s: %w", item.Name, err)
		}
	case actionSkip, actionConflict, actionLeaveUnstamped:
		// no write
	}
	return nil
}

// readStampedLocalSkills returns every locally-materialised (stamped) skill
// under skillsRoot, so a substrate removal can be detected as a local
// orphan. Unstamped dirs are skipped — they're operator-authored.
func readStampedLocalSkills(skillsRoot string) ([]localSkill, error) {
	entries, err := os.ReadDir(skillsRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []localSkill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		l, err := readLocalSkill(skillsRoot, e.Name())
		if err != nil {
			return nil, err
		}
		if l != nil && l.Stamped {
			out = append(out, *l)
		}
	}
	return out, nil
}

// verbDispatch is the seam sync calls the substrate through. The command
// binds it to dispatchVerb (config-aware HTTP/in-process routing); a test
// injects a fake to assert the requests sync builds (sty_ab160e22 AC2).
type verbDispatch func(ctx context.Context, name string, req json.RawMessage) (json.RawMessage, error)

// firstNonEmpty returns a if it is non-empty, else b — used to prefer the id
// a list row reports, falling back to the command-level filter value.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// listSubstrateSkills pulls the type:skill rows for the scope and fetches
// each body via document_get (the body carries authored frontmatter intact,
// sty_4b517016). Reuses the existing list/get verbs — no new MCP surface.
//
// document_get enforces authorizeRead, which requires workspace_id for
// workspace/project scope; document_list does not. So each get is keyed on
// the scope + ids the row itself reports (falling back to the command-level
// filter when a row omits one) — a default-flag `skill sync` then resolves
// the workspace_id document_list never demanded (sty_ab160e22).
// listSubstrateSkills lists the substrate skills at a scope and fetches each
// body. When effective is true the caller's user-scope overrides are overlaid
// (sty_cbeeb452) — the dynamic skill index passes true so a user's overridden
// workflow/gate skill wins; sync passes false so it reconciles the shared rows
// against the operator's stamped local tree, not per-caller overrides.
func listSubstrateSkills(ctx context.Context, dispatch verbDispatch, scope, wsID, pjID string, effective bool) ([]substrateSkill, error) {
	listReq, err := json.Marshal(docListRequest{Type: "skill", Scope: scope, WorkspaceID: wsID, ProjectID: pjID, Limit: 200, Effective: effective})
	if err != nil {
		return nil, err
	}
	raw, err := dispatch(ctx, "document_list", listReq)
	if err != nil {
		return nil, fmt.Errorf("skill sync: list: %w", err)
	}
	var listed struct {
		Items []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			Scope         string `json:"scope"`
			WorkspaceID   string `json:"workspace_id"`
			ProjectID     string `json:"project_id"`
			LatestVersion int    `json:"latest_version"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		return nil, fmt.Errorf("skill sync: decode list: %w", err)
	}
	out := make([]substrateSkill, 0, len(listed.Items))
	for _, it := range listed.Items {
		getReq, err := json.Marshal(struct {
			Name        string `json:"name"`
			Scope       string `json:"scope,omitempty"`
			WorkspaceID string `json:"workspace_id,omitempty"`
			ProjectID   string `json:"project_id,omitempty"`
		}{
			Name:        it.Name,
			Scope:       firstNonEmpty(it.Scope, scope),
			WorkspaceID: firstNonEmpty(it.WorkspaceID, wsID),
			ProjectID:   firstNonEmpty(it.ProjectID, pjID),
		})
		if err != nil {
			return nil, err
		}
		gotRaw, err := dispatch(ctx, "document_get", getReq)
		if err != nil {
			return nil, fmt.Errorf("skill sync: get %q: %w", it.Name, err)
		}
		var got struct {
			RawBody  string `json:"raw_body"`
			Document struct {
				ID            string `json:"id"`
				LatestVersion int    `json:"latest_version"`
			} `json:"document"`
		}
		if err := json.Unmarshal(gotRaw, &got); err != nil {
			return nil, fmt.Errorf("skill sync: decode get %q: %w", it.Name, err)
		}
		out = append(out, substrateSkill{
			Name:       it.Name,
			DocumentID: got.Document.ID,
			Version:    got.Document.LatestVersion,
			Body:       got.RawBody,
		})
	}
	return out, nil
}
