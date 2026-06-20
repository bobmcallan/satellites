// Workflow sync (sty_fc39ca77 AC3) — the pull half for server-stored workflows.
//
// A kind:workflow row MUST materialise to .satellites/workflows/<name>.md (flat),
// NOT .claude/skills/<name>/SKILL.md. Reason: the governing-workflow resolver
// reads BOTH clientWorkflows (.satellites/workflows) AND materialisedWorkflowSources
// (.claude/skills kind:workflow); a synced workflow landing in .claude/skills would
// be read from both homes → duplicate/drift. So .satellites/workflows is the single
// home, and this is a parallel reconcile to the skill one (same stamp identity,
// same reconcileAction verdicts) targeting flat files instead of <name>/SKILL.md.
//
// It runs as the tail of syncSkills, so `satellites skill sync` and `satellites
// deploy` both pull workflows with no separate command. An operator-authored
// workflow with no server row is left untouched (actionLeaveUnstamped); a stamped
// one the server advanced is updated; one whose row is gone is removed.

package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// listSubstrateWorkflows lists the kind:workflow rows at a scope. It reuses
// listSubstrateSkills (every workflow stores as a type:skill row) and keeps only
// the kind:workflow bodies — no new list path.
func listSubstrateWorkflows(ctx context.Context, dispatch verbDispatch, scope, wsID, pjID string) ([]substrateSkill, error) {
	all, err := listSubstrateSkills(ctx, dispatch, scope, wsID, pjID, false)
	if err != nil {
		return nil, err
	}
	var out []substrateSkill
	for _, s := range all {
		if isWorkflowBody(s.Body) {
			out = append(out, s)
		}
	}
	return out, nil
}

// readLocalWorkflow loads .satellites/workflows/<name>.md and classifies it for
// the reconcile (stamped? edited? legacy layout?). Returns nil when absent.
// Mirrors readLocalSkill but for a flat file, not <name>/SKILL.md.
func readLocalWorkflow(dir, name string) (*localSkill, error) {
	raw, err := os.ReadFile(filepath.Join(dir, name+".md"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	stamp, authored, ok, malformed, legacy := splitStamp(string(raw))
	return &localSkill{Name: name, Stamped: ok, Stamp: stamp, Malformed: malformed, Legacy: legacy, BodyHash: hashBody(authored)}, nil
}

// readStampedLocalWorkflows returns every stamped (sync-materialised) workflow
// file under dir, so a substrate removal is detected as a local orphan. Unstamped
// files are operator-authored and skipped — the sub==nil branch leaves them.
func readStampedLocalWorkflows(dir string) ([]localSkill, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []localSkill
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		l, err := readLocalWorkflow(dir, strings.TrimSuffix(e.Name(), ".md"))
		if err != nil {
			return nil, err
		}
		if l != nil && l.Stamped {
			out = append(out, *l)
		}
	}
	return out, nil
}

// reconcileWorkflows computes the per-name verdict across the union of the
// substrate workflows and the locally-materialised ones, keyed on the row name
// as-is (a workflow file is <name>.md — no satellites- prefix is forced the way
// localSkillName does for .claude/skills). reconcileAction is the same pure core
// the skill sync uses.
func reconcileWorkflows(subs []substrateSkill, locals []localSkill) []syncPlanItem {
	subByName := map[string]substrateSkill{}
	for _, s := range subs {
		subByName[s.Name] = s
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

// applyWorkflowSyncItem performs one verdict against a flat .satellites/workflows
// file. install/update/adopt/migrate write the materialised, stamped file; remove
// deletes it; skip/conflict/leave make no change.
func applyWorkflowSyncItem(dir string, item syncPlanItem) error {
	path := filepath.Join(dir, item.Name+".md")
	switch item.Action {
	case actionInstall, actionUpdate, actionMatchUnstamped, actionMigrate:
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
		if err := os.WriteFile(path, []byte(materialise(*item.Sub)), 0o644); err != nil {
			return fmt.Errorf("write workflow %s: %w", item.Name, err)
		}
	case actionRemove:
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove workflow %s: %w", item.Name, err)
		}
	case actionSkip, actionConflict, actionLeaveUnstamped:
		// no write
	}
	return nil
}

// syncWorkflows pulls every resolvable-scope kind:workflow row and reconciles it
// into .satellites/workflows/ (the single governing home). Runs as the tail of
// syncSkills so the existing sync entry points cover it. Merge precedence project
// > workspace > system (later wins), matching the skill cascade.
func syncWorkflows(ctx context.Context, out io.Writer, dispatch verbDispatch, ws, pj, dir string, dryRun bool) error {
	var subs []substrateSkill
	sys, err := listSubstrateWorkflows(ctx, dispatch, "system", "", "")
	if err != nil {
		return err
	}
	subs = append(subs, sys...)
	if strings.TrimSpace(ws) != "" {
		wsw, err := listSubstrateWorkflows(ctx, dispatch, "workspace", ws, "")
		if err != nil {
			return err
		}
		subs = append(subs, wsw...)
	}
	if strings.TrimSpace(pj) != "" {
		pjw, err := listSubstrateWorkflows(ctx, dispatch, "project", ws, pj)
		if err != nil {
			return err
		}
		subs = append(subs, pjw...)
	}
	// Merge by name; append order sys → workspace → project means the project row
	// wins a name collision (most-specific-wins).
	byName := map[string]substrateSkill{}
	for _, s := range subs {
		byName[s.Name] = s
	}
	merged := make([]substrateSkill, 0, len(byName))
	for _, s := range byName {
		merged = append(merged, s)
	}

	localByName := map[string]localSkill{}
	stamped, err := readStampedLocalWorkflows(dir)
	if err != nil {
		return err
	}
	for _, l := range stamped {
		localByName[l.Name] = l
	}
	for _, s := range merged {
		if _, seen := localByName[s.Name]; seen {
			continue
		}
		if l, err := readLocalWorkflow(dir, s.Name); err != nil {
			return err
		} else if l != nil {
			localByName[s.Name] = *l
		}
	}
	locals := make([]localSkill, 0, len(localByName))
	for _, l := range localByName {
		locals = append(locals, l)
	}

	for _, item := range reconcileWorkflows(merged, locals) {
		if dryRun {
			fmt.Fprintf(out, "[dry-run] workflow %-9s %s  (%s)\n", item.Action, item.Name, item.Action.reason())
			continue
		}
		// A conflict (a stamped local workflow edited since materialisation) is kept
		// local — a workflow is repo config an operator may have customised;
		// surfacing beats clobbering. Re-upsert to reconcile.
		if item.Action == actionConflict {
			fmt.Fprintf(out, "workflow conflict %s — kept local (edit differs from substrate; re-upsert to reconcile)\n", item.Name)
			continue
		}
		if err := applyWorkflowSyncItem(dir, item); err != nil {
			return err
		}
		if item.Action != actionSkip && item.Action != actionLeaveUnstamped {
			fmt.Fprintf(out, "workflow %-9s %s  (%s)\n", item.Action, item.Name, item.Action.reason())
		}
	}
	return nil
}
