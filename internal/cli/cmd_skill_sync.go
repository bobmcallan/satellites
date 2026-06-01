// `satellites skill sync` — the pull half of the source round-trip
// (sty_52e10340). Where `skill upload` pushes config/ sources → substrate,
// `sync` pulls substrate type:skill rows → .claude/skills/<name>/SKILL.md so
// the gate runs the materialised install, and editing a rubric in the
// substrate takes effect after a sync with no hand-edit and no binary
// release.
//
// Ownership is keyed by an injected identity stamp, NOT the skill name
// prefix (sty_4b517016 built the stamp; operator decision on sty_52e10340).
// sync only ever updates or removes a local skill it materialised (one
// carrying the stamp); an unstamped local skill is operator-authored and is
// never touched. Pull-only: it never writes back to the substrate.
//
// Composes the existing document_list / document_get verbs — no new MCP verb
// (no-new-mcp-verbs).

package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// stampMarker delimits the injected sync-identity block at the top of a
// materialised SKILL.md. The block sits above the authored body and is
// excluded from the content hash, so the hash reflects only what the
// substrate stores (sty_4b517016 contract).
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
	actionConflict                         // stamped local copy edited (hash mismatch) — refuse to overwrite
	actionLeaveUnstamped                   // local copy with no stamp — operator-authored, never touch
)

func (a syncAction) String() string {
	switch a {
	case actionInstall:
		return "install"
	case actionUpdate:
		return "update"
	case actionSkip:
		return "skip"
	case actionRemove:
		return "remove"
	case actionConflict:
		return "conflict"
	case actionLeaveUnstamped:
		return "leave"
	}
	return "?"
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

// splitStamp separates a materialised SKILL.md into its injected stamp (if
// present + parseable) and the authored body beneath it. A file with no
// stamp marker returns ok=false and the whole content as the authored body.
func splitStamp(raw string) (stamp skillStamp, authored string, ok bool, malformed bool) {
	i := strings.Index(raw, stampBegin)
	if i != 0 {
		// Stamp must be the very first bytes; anything else is unstamped.
		return skillStamp{}, raw, false, false
	}
	end := strings.Index(raw, stampEnd)
	if end < 0 {
		return skillStamp{}, raw, false, true
	}
	jsonPart := strings.TrimSpace(raw[len(stampBegin):end])
	authoredStart := end + len(stampEnd)
	authored = strings.TrimPrefix(raw[authoredStart:], "\n")
	var st skillStamp
	if err := json.Unmarshal([]byte(jsonPart), &st); err != nil || st.DocumentID == "" {
		return skillStamp{}, authored, false, true
	}
	return st, authored, true, false
}

// materialise renders the on-disk SKILL.md content for a substrate skill:
// the injected stamp block followed by the authored body.
func materialise(s substrateSkill) string {
	stamp := skillStamp{DocumentID: s.DocumentID, Version: s.Version, Hash: hashBody(s.Body)}
	return stampBlock(stamp) + s.Body
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
			// Operator authored a skill with this name; never clobber.
			return actionLeaveUnstamped
		}
		if local.Malformed {
			return actionConflict
		}
		if local.BodyHash != local.Stamp.Hash {
			// Operator edited a materialised copy since it was stamped.
			return actionConflict
		}
		if sub.Version > local.Stamp.Version {
			return actionUpdate
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
	stamp, authored, ok, malformed := splitStamp(string(raw))
	ls := &localSkill{Name: name, Stamped: ok, Stamp: stamp, Malformed: malformed}
	if ok {
		ls.BodyHash = hashBody(authored)
	}
	return ls, nil
}

// reconcileSkills computes the per-name verdict across the union of the
// substrate set and the locally-materialised set. Local names are limited to
// those that carry a stamp OR appear in the substrate — an unstamped local
// skill not in the substrate is operator-authored and simply omitted (sync
// has no business with it). Deterministic order for stable output + tests.
func reconcileSkills(subs []substrateSkill, locals []localSkill) []syncPlanItem {
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
		plan = append(plan, syncPlanItem{Name: n, Action: reconcileAction(subPtr, localPtr), Sub: subPtr})
	}
	return plan
}

// syncPlanItem is one reconcile verdict the command applies.
type syncPlanItem struct {
	Name   string
	Action syncAction
	Sub    *substrateSkill // non-nil for install/update
}

func newSkillSyncCmd(configArg, userArg *string) *cobra.Command {
	var (
		scopeArg string
		wsArg    string
		pjArg    string
		dryRun   bool
		root     string
	)
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Materialise substrate skills into .claude/skills/ (pull-only, stamp-reconciled)",
		Long: `sync pulls type:skill rows from the substrate into
.claude/skills/<name>/SKILL.md so the gate runs the materialised install.

Pull-only and stamp-keyed: sync only updates or removes a local skill it
materialised (one carrying the injected identity stamp); an operator-authored
skill with no stamp is never touched. Editing a rubric in the substrate and
re-running sync takes effect with no hand-edit and no binary release.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			skillsRoot := root
			if skillsRoot == "" {
				skillsRoot = filepath.Join(".claude", "skills")
			}

			dispatch := func(ctx context.Context, name string, req json.RawMessage) (json.RawMessage, error) {
				return dispatchVerb(ctx, name, req, *configArg, *userArg)
			}
			subs, err := listSubstrateSkills(ctx, dispatch, scopeArg, wsArg, pjArg)
			if err != nil {
				return err
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

			plan := reconcileSkills(subs, locals)
			out := cmd.OutOrStdout()
			for _, item := range plan {
				if dryRun {
					fmt.Fprintf(out, "[dry-run] %-8s %s\n", item.Action, item.Name)
					continue
				}
				if err := applySyncItem(skillsRoot, item); err != nil {
					return err
				}
				fmt.Fprintf(out, "%-8s %s\n", item.Action, item.Name)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&scopeArg, "scope", "project", "Scope to sync (system / workspace / project)")
	cmd.Flags().StringVar(&wsArg, "workspace", "", "workspace_id (required for scope=workspace or project)")
	cmd.Flags().StringVar(&pjArg, "project", "", "project_id (required for scope=project)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print the reconcile plan without writing files")
	cmd.Flags().StringVar(&root, "skills-root", "", "Materialisation root (default .claude/skills)")
	return cmd
}

// applySyncItem performs the verdict for one plan item. Conflicts, skips,
// and leaves are reported but make no change.
func applySyncItem(skillsRoot string, item syncPlanItem) error {
	switch item.Action {
	case actionInstall, actionUpdate:
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
func listSubstrateSkills(ctx context.Context, dispatch verbDispatch, scope, wsID, pjID string) ([]substrateSkill, error) {
	listReq, err := json.Marshal(docListRequest{Type: "skill", Scope: scope, WorkspaceID: wsID, ProjectID: pjID, Limit: 200})
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
