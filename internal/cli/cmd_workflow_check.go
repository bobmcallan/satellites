// `satellites workflow check` — the process-drift validator (sty_11e09ae7).
// Read-only and fail-closed: it reconciles the DEFINED process (workflow
// skills, gate skills, the materialised skill tree, the project's stories)
// against itself and reports every mechanical drift class this epic's audit
// found by hand. Exit 0 CLEAN / exit 1 BLOCKED; advisory findings report
// without blocking.
//
// Every check is a pure function over parsed inputs so fixtures can replay
// each drift class in unit tests. The MCP-door half of the surface contract
// (skill/principle writes/reads/deletes refused over MCP) is enforced
// server-side and pinned by its integration test — a client check cannot
// probe it and does not pretend to.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workflow"
	"github.com/spf13/cobra"
)

// markLocalAuthorship flags each skill authored in THIS repo's skill authoring
// dir (<skills_root>/skills/<name>.md) as repo-owned (matSkill.local). A skill
// only present under .claude/skills (materialised by sync from a publisher or
// the system baseline) is INHERITED — a palette. The orphan-gate rule flags
// only repo-owned, unwired gates; inherited gates a workflow names none of are
// an opt-in palette, not drift (sty_f8f88f92). Mutates and returns skills.
func markLocalAuthorship(skills []matSkill, configArg string) []matSkill {
	dir := filepath.Join(resolveSubstrateRoot("skills", configArg), "skills")
	local := map[string]bool{}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			local[strings.TrimSuffix(e.Name(), ".md")] = true
		}
	}
	for i := range skills {
		if local[skills[i].name] {
			skills[i].local = true
		}
	}
	return skills
}

// driftFinding is one reported drift. Severity is "block" or "advise".
type driftFinding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Artifact string `json:"artifact"`
	Message  string `json:"message"`
}

// storyLite is the minimal story projection the governance check reads.
type storyLite struct {
	ID       string
	Name     string
	Category string
	Status   string
	Body     string
}

// checkpointGateRe pulls [[gate-name]] references out of a workflow body's
// "## Checkpoint gates" section — the definition's commit-time gate list.
var checkpointGateRe = regexp.MustCompile(`\[\[([a-z0-9-]+)\]\]`)

// checkpointGates returns the gate names a workflow body's Checkpoint gates
// section references. Empty when the section is absent.
func checkpointGates(body string) []string {
	i := strings.Index(body, "## Checkpoint gates")
	if i < 0 {
		return nil
	}
	section := body[i:]
	if j := strings.Index(section[3:], "\n## "); j >= 0 {
		section = section[:j+3]
	}
	var out []string
	for _, m := range checkpointGateRe.FindAllStringSubmatch(section, -1) {
		out = append(out, m[1])
	}
	return out
}

// checkSyncedSkillUsable (class 4 — Claude-unusable synced skills): every
// materialised SKILL.md must open with its YAML frontmatter (a sync stamp or
// anything else above it hides the skill from Claude Code) and expose a
// non-empty description (without one the harness shows the first body line).
func checkSyncedSkillUsable(skills []matSkill) []driftFinding {
	var out []driftFinding
	for _, s := range skills {
		if !strings.HasPrefix(s.raw, "---\n") && !strings.HasPrefix(s.raw, "---\r\n") {
			out = append(out, driftFinding{"block", "unusable-skill", s.name,
				"SKILL.md does not open with frontmatter — Claude Code cannot read its contract (sync stamp above frontmatter?)"})
			continue
		}
		if s.description == "" {
			out = append(out, driftFinding{"block", "unusable-skill", s.name,
				"no description in frontmatter — the skill list falls back to the first body line and the skill is undispatchable"})
		}
	}
	return out
}

// parseWorkflows extracts every kind:workflow skill's parsed definition,
// reporting class 6 (workflow soundness) findings for the unparseable or
// degenerate ones.
func parseWorkflows(skills []matSkill) (map[string]*workflow.Workflow, []driftFinding) {
	wfs := map[string]*workflow.Workflow{}
	var out []driftFinding
	for _, s := range skills {
		if s.kind != "workflow" {
			continue
		}
		wf, err := workflow.Parse([]byte(s.raw))
		if err != nil || wf == nil {
			out = append(out, driftFinding{"block", "workflow-lifecycle", s.name,
				"kind:workflow skill carries no parseable states/transitions block"})
			continue
		}
		if err := wf.ValidateLifecycle(); err != nil {
			out = append(out, driftFinding{"block", "workflow-lifecycle", s.name, err.Error()})
			continue
		}
		wfs[s.name] = wf
	}
	return wfs, out
}

// checkGateCoverage (classes 1 + 5) — the definition is the whole process:
// every kind:gate skill must be named by some workflow (a transition's
// reviewer_skill or a Checkpoint gates reference); a gate nothing names is a
// shadow gate that runs outside any definition (the techdebt case). And every
// gate a workflow names must be materialised, or its transition fails closed
// at dispatch.
func checkGateCoverage(skills []matSkill, wfs map[string]*workflow.Workflow, gateResolvable func(string) bool) []driftFinding {
	named := map[string]bool{}
	for name, wf := range wfs {
		for _, tr := range wf.Transitions {
			if g := strings.TrimSpace(tr.ReviewerSkill); g != "" {
				named[g] = true
			}
		}
		for _, s := range skills {
			if s.name == name {
				for _, g := range checkpointGates(s.body) {
					named[g] = true
				}
			}
		}
	}
	byName := map[string]matSkill{}
	for _, s := range skills {
		byName[s.name] = s
	}

	var out []driftFinding
	for _, s := range skills {
		if s.kind != "gate" {
			continue
		}
		// An INHERITED gate (materialised by sync from a publisher/system) that
		// no workflow names is an opt-in PALETTE entry, not drift — under the
		// gateless baseline a clean consumer inherits gates it names none of.
		// Only a REPO-OWNED gate (authored here, s.local) that nothing wires is a
		// genuine shadow gate (sty_f8f88f92).
		if !named[s.name] && s.local {
			out = append(out, driftFinding{"block", "orphan-gate", s.name,
				"repo-owned kind:gate skill no workflow definition names (transition or Checkpoint gates) — a shadow gate runs outside the defined process; wire it into a workflow or remove it"})
		}
	}
	for g := range named {
		// A gate the dispatcher can resolve is AVAILABLE even when not
		// materialised to .claude/skills: an internal embedded gate (satellites'
		// own governance, injected from the binary) OR a substrate gate fetched
		// from the server (sty_f242eacf). gateResolvable folds both; only a gate
		// that resolves from NO tier is a missing gate (2.4.1).
		if _, ok := byName[g]; !ok && !gateResolvable(g) {
			out = append(out, driftFinding{"block", "missing-gate", g,
				"a workflow names this reviewer skill but it resolves from no source — not embedded, not in .claude/skills, not on the server — its transition fails closed"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Artifact < out[j].Artifact })
	return out
}

// checkGatePlacementConflict (class 8): when a workflow binds a gate's
// execution to a state (the `command:` rider on an actor:satellites state),
// that command has exactly one execution home — the state's traverse, with the
// gate skill owning the decision rule. A non-gate skill whose body instructs
// running the same command is a second placement claim: an executor following
// it runs the gate outside its state and the two documents drift apart (the
// commit-push/techdebt contradiction). Reference the gate by [[name]] instead.
func checkGatePlacementConflict(skills []matSkill, wfs map[string]*workflow.Workflow) []driftFinding {
	type binding struct{ wf, state string }
	commands := map[string]binding{}
	for name, wf := range wfs {
		for _, st := range wf.States {
			if c := strings.TrimSpace(st.Command); c != "" {
				commands[c] = binding{wf: name, state: st.Name}
			}
		}
	}
	var out []driftFinding
	for _, s := range skills {
		if s.kind == "gate" || s.kind == "workflow" {
			continue
		}
		for c, b := range commands {
			if strings.Contains(s.body, c) {
				out = append(out, driftFinding{"block", "gate-placement-conflict", s.name,
					fmt.Sprintf("skill body instructs running %q, which workflow %s binds to state %q — the state's traverse is the command's single execution home; reference the gate skill instead of restating its run", c, b.wf, b.state)})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Artifact != out[j].Artifact {
			return out[i].Artifact < out[j].Artifact
		}
		return out[i].Message < out[j].Message
	})
	return out
}

// nonAtomicMarkers flag a non-gate skill that looks like it embeds a
// fail-closed verdict routine (class 2). Advisory: the authoritative judge is
// `satellites skill review` item 8 — this points at candidates.
var nonAtomicMarkers = []string{"Exit 1 (BLOCKED)", "exit 1 → **do not commit**", "fail closed"}

func checkNonAtomicCandidates(skills []matSkill) []driftFinding {
	var out []driftFinding
	for _, s := range skills {
		if s.kind == "gate" || s.kind == "workflow" {
			continue
		}
		for _, m := range nonAtomicMarkers {
			if strings.Contains(strings.ToLower(s.body), strings.ToLower(m)) {
				out = append(out, driftFinding{"advise", "nonatomic-candidate", s.name,
					fmt.Sprintf("non-gate skill body carries a fail-closed verdict marker (%q) — run `satellites skill review` (atomicity item) to judge whether it embeds a gate", m)})
				break
			}
		}
	}
	return out
}

// hostCoupledPatterns are repo-dev specifics a system-scope artifact must not
// reference (class 3). Deliberately narrow: a product's own distribution
// contract (release URLs, install paths) is not host coupling and none of
// these patterns match it.
var hostCoupledPatterns = []string{".github/workflows", ".version", "internal/cli", "cmd/satellites", "fly.dev"}

func checkSystemScopeCoupling(skills []matSkill) []driftFinding {
	var out []driftFinding
	for _, s := range skills {
		if !strings.EqualFold(s.scope, "system") {
			continue
		}
		for _, p := range hostCoupledPatterns {
			if strings.Contains(s.body, p) {
				out = append(out, driftFinding{"block", "host-coupled-system", s.name,
					fmt.Sprintf("system-scope skill references repo-dev specific %q — system scope must work in any repository (re-scope to project or rewrite)", p)})
				break
			}
		}
	}
	return out
}

// terminalStoryStatuses are statuses with no outgoing work anywhere; a story
// resting in one needs no governing workflow resolution.
var terminalStoryStatuses = map[string]bool{"done": true, "cancelled": true, "deleted": true}

// checkStoryGovernance (class 5) — every non-terminal story is governed:
// its own embedded ## Workflow (whose reviewer skills must be materialised),
// or a workflow whose applies_to covers its category ("*" wildcard counts).
func checkStoryGovernance(stories []storyLite, skills []matSkill, wfs map[string]*workflow.Workflow, gateResolvable func(string) bool) []driftFinding {
	byName := map[string]bool{}
	for _, s := range skills {
		byName[s.name] = true
	}
	covered := func(category string) bool {
		for _, wf := range wfs {
			for _, at := range wf.AppliesTo {
				at = strings.TrimSpace(at)
				if at == "*" || strings.EqualFold(at, category) {
					return true
				}
			}
		}
		return false
	}
	var out []driftFinding
	for _, st := range stories {
		if terminalStoryStatuses[strings.ToLower(st.Status)] {
			continue
		}
		wf, err := workflow.ParseBody([]byte(st.Body))
		if err == nil && wf != nil {
			for _, tr := range wf.Transitions {
				// A gate the dispatcher resolves — embedded in the binary OR
				// fetched from the server (sty_f242eacf) — lets the story move even
				// when not materialised; only a gate that resolves from no tier is
				// unresolvable (2.4.1).
				if g := strings.TrimSpace(tr.ReviewerSkill); g != "" && !byName[g] && !gateResolvable(g) {
					out = append(out, driftFinding{"block", "unresolvable-gate", st.ID,
						fmt.Sprintf("embedded workflow names reviewer %q which resolves from no source (not embedded, not materialised, not on the server) — the story cannot move", g)})
				}
			}
			continue
		}
		if !covered(st.Category) {
			out = append(out, driftFinding{"block", "ungoverned-story", st.ID,
				fmt.Sprintf("no embedded ## Workflow and no workflow's applies_to covers category %q — the story has no defined process", st.Category)})
		}
	}
	return out
}

// firstGateLayers are the comprehensive-review layers the entry gate must
// judge (class 7, surface contract): a story is created with no review, so
// the FIRST gate carries the whole judgment.
var firstGateLayers = []string{"shape", "plan", "acceptance", "workflow", "grounding"}

func checkFirstGateComprehensive(skills []matSkill, wfs map[string]*workflow.Workflow) []driftFinding {
	byName := map[string]matSkill{}
	for _, s := range skills {
		byName[s.name] = s
	}
	var out []driftFinding
	for wfName, wf := range wfs {
		entry := wf.InitialState()
		if entry == "" {
			continue
		}
		for _, tr := range wf.Transitions {
			if tr.From != entry || strings.TrimSpace(tr.ReviewerSkill) == "" {
				continue
			}
			gate, ok := byName[tr.ReviewerSkill]
			if !ok {
				continue // missing-gate already reported
			}
			// A cancellation edge is not the review path; judge only edges
			// that move work forward (target has outgoing transitions).
			forward := false
			for _, tr2 := range wf.Transitions {
				if tr2.From == tr.To {
					forward = true
					break
				}
			}
			if !forward {
				continue
			}
			lower := strings.ToLower(gate.body)
			var missing []string
			for _, l := range firstGateLayers {
				if !strings.Contains(lower, l) {
					missing = append(missing, l)
				}
			}
			if len(missing) > 0 {
				out = append(out, driftFinding{"block", "first-gate-shallow", gate.name,
					fmt.Sprintf("entry gate of %s does not cover comprehensive-review layer(s) %v — the first gate is where an unreviewed story is judged", wfName, missing)})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Artifact < out[j].Artifact })
	return out
}

// checkAmbiguousGovernance (multi-workflow resolution, sty_0889de7a): the
// governing workflow for a story resolves by applies_to ↔ category, so a
// category must map to exactly ONE non-wildcard workflow. Two workflows both
// claiming the same specific category make resolution ambiguous — fail closed
// (the "*" wildcard is the shared default and never conflicts).
func checkAmbiguousGovernance(wfs map[string]*workflow.Workflow) []driftFinding {
	byCat := map[string][]string{}
	for name, wf := range wfs {
		for _, at := range wf.AppliesTo {
			at = strings.TrimSpace(at)
			if at == "" || at == "*" {
				continue
			}
			cat := strings.ToLower(at)
			byCat[cat] = append(byCat[cat], name)
		}
	}
	var out []driftFinding
	for cat, names := range byCat {
		if len(names) > 1 {
			sort.Strings(names)
			out = append(out, driftFinding{"block", "ambiguous-governance", strings.Join(names, ","),
				fmt.Sprintf("category %q is covered by %d workflows (%s) — applies_to↔category resolution is ambiguous; a category maps to exactly one governing workflow", cat, len(names), strings.Join(names, ", "))})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Artifact < out[j].Artifact })
	return out
}

// runWorkflowChecks composes every drift check over the supplied corpus —
// the pure core the unit fixtures replay. `skills` is the materialised
// .claude/skills set; `clientWorkflows` is the client-dir workflow config set
// (.satellites/workflows, kind:workflow). Skill-only checks (synced-usable,
// non-atomic, system-coupling) run over `skills`; every workflow-bearing check
// runs over the merged set so a client-dir workflow is governed, its named
// gates are covered, and its checkpoint-gate references resolve — exactly as a
// kind:workflow skill was (epic:client-dir-separation order-2).
func runWorkflowChecks(skills []matSkill, clientWorkflows []matSkill, stories []storyLite) []driftFinding {
	// Default to embed-only gate resolution (verb.IsInternalGate): a named gate
	// is available if materialised locally OR embedded in the binary. The command
	// entry uses runWorkflowChecksResolved to add the server tier; the unit
	// fixtures keep replaying this embed-only core.
	return runWorkflowChecksResolved(skills, clientWorkflows, stories, verb.IsInternalGate)
}

// runWorkflowChecksResolved is runWorkflowChecks with the gate-resolution
// predicate injected. gateResolvable reports whether a NAMED reviewer gate
// resolves from a source beyond the materialised set — embedded, or (in prod)
// the server (sty_f242eacf) — so the gate-coverage and story-governance checks
// agree with the dispatcher's embed → local → server resolution and do not flag
// a gate the dispatcher can run.
func runWorkflowChecksResolved(skills []matSkill, clientWorkflows []matSkill, stories []storyLite, gateResolvable func(string) bool) []driftFinding {
	wfBearing := append(append([]matSkill{}, skills...), clientWorkflows...)
	var out []driftFinding
	out = append(out, checkSyncedSkillUsable(skills)...)
	wfs, wfFindings := parseWorkflows(wfBearing)
	out = append(out, wfFindings...)
	out = append(out, checkAmbiguousGovernance(wfs)...)
	out = append(out, checkGateCoverage(wfBearing, wfs, gateResolvable)...)
	out = append(out, checkGatePlacementConflict(wfBearing, wfs)...)
	out = append(out, checkGateHomeFork(skills)...)
	out = append(out, checkNonAtomicCandidates(skills)...)
	out = append(out, checkSystemScopeCoupling(skills)...)
	out = append(out, checkStoryGovernance(stories, wfBearing, wfs, gateResolvable)...)
	out = append(out, checkFirstGateComprehensive(wfBearing, wfs)...)
	return out
}

// checkGateHomeFork enforces the home-of-gate invariant (epic:minimal-spine
// order-8): a gate resolves from EXACTLY one home — the embedded spine OR the
// materialised .claude/skills, never both. A name that is both embedded
// (verb.IsInternalGate) and materialised is a divergent shadow: the dispatcher
// resolves the embedded copy first, so the materialised one is silently dead.
// This is a structural wiring invariant, like orphan-gate / missing-gate — no
// allowlist, just one-home-per-gate. Fail closed on any fork so the "fork the
// source of truth" failure cannot silently return.
func checkGateHomeFork(skills []matSkill) []driftFinding {
	var out []driftFinding
	for _, s := range skills {
		if verb.IsInternalGate(s.name) {
			out = append(out, driftFinding{"block", "gate-home-fork", s.name,
				fmt.Sprintf("gate %q is BOTH embedded in the binary and materialised in .claude/skills — the dispatcher resolves the embedded copy first, so the materialised one is a dead, divergent shadow. Give it ONE home: an embedded spine gate must be removed from .satellites/skills and the server (so it resolves only from the embed); a substrate gate must not be embedded.", s.name)})
		}
	}
	return out
}

// listProjectStories pulls the repo project's stories (id, category, status,
// body) for the governance check; an unconfigured repo yields none.
func listProjectStories(ctx context.Context, configArg, userArg string) ([]storyLite, error) {
	_, pj, err := resolveDeployScope(ctx, configArg, userArg)
	if err != nil || strings.TrimSpace(pj) == "" {
		return nil, nil
	}
	listReq, _ := json.Marshal(docListRequest{Type: "story", ProjectID: pj, Limit: 200})
	raw, err := dispatchVerb(ctx, "document_list", listReq, configArg, userArg)
	if err != nil {
		return nil, fmt.Errorf("workflow check: list stories: %w", err)
	}
	var listed struct {
		Items []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Category string `json:"category"`
			Status   string `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		return nil, fmt.Errorf("workflow check: decode list: %w", err)
	}
	var out []storyLite
	for _, it := range listed.Items {
		if terminalStoryStatuses[strings.ToLower(it.Status)] {
			continue
		}
		getReq, _ := json.Marshal(struct {
			ID string `json:"id"`
		}{it.ID})
		graw, gErr := dispatchVerb(ctx, "document_get", getReq, configArg, userArg)
		if gErr != nil {
			continue
		}
		var got struct {
			RawBody string `json:"raw_body"`
		}
		if json.Unmarshal(graw, &got) != nil {
			continue
		}
		out = append(out, storyLite{ID: it.ID, Name: it.Name, Category: it.Category, Status: it.Status, Body: got.RawBody})
	}
	return out, nil
}

func newWorkflowCheckCmd(configArg, userArg *string) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Validate the configured process for drift (defined workflow vs executable reality) — exit 1 on any blocking finding",
		Long: `check reconciles the defined process against itself, read-only:

  unusable-skill        a materialised SKILL.md Claude Code cannot dispatch
  workflow-lifecycle    a kind:workflow skill with a degenerate state machine
  orphan-gate           a kind:gate skill no workflow definition names (shadow gate)
  missing-gate          a workflow names a reviewer skill that is not materialised
  gate-placement-conflict  a non-gate skill instructs running a command a workflow binds to a state
  nonatomic-candidate   (advisory) a non-gate skill carrying fail-closed verdict markers
  host-coupled-system   a system-scope skill referencing repo-dev specifics
  ungoverned-story      a non-terminal story with no embedded workflow and no applies_to cover
  ambiguous-governance  two non-wildcard workflows cover the same story category
  unresolvable-gate     a story's embedded workflow names a non-materialised reviewer
  first-gate-shallow    a workflow's entry gate skips comprehensive-review layers

Blocking findings exit 1; advisory findings report and pass. The MCP write/
read/delete bounds on skills and principles are enforced server-side and
pinned by integration tests — they are out of a client check's reach.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			start := time.Now()
			stories, err := listProjectStories(ctx, *configArg, *userArg)
			if err != nil {
				return err
			}
			// Resolve named gates the way the dispatcher does — embed → local →
			// server (sty_f242eacf) — so a substrate gate pruned from
			// .claude/skills but present on the server is not flagged.
			fetch := serverGateFetcher(*configArg, *userArg)
			gateResolvable := func(g string) bool { return verb.GateResolvable(ctx, fetch, ".", g) }
			findings := runWorkflowChecksResolved(markLocalAuthorship(materialisedSkills(), *configArg), clientWorkflows(*configArg), stories, gateResolvable)
			blocking := 0
			for _, f := range findings {
				if f.Severity == "block" {
					blocking++
				}
			}
			verdict := gateVerdictClean
			if blocking > 0 {
				verdict = gateVerdictBlocked
			}
			recordGateVerdict(ctx, *configArg, *userArg, "workflow-check", verdict, blocking, time.Since(start), cmd.OutOrStdout())
			return reportWorkflowChecks(cmd.OutOrStdout(), findings, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Emit findings as a JSON array")
	return cmd
}

// reportWorkflowChecks prints the findings and returns a non-nil error (exit
// 1) when any blocking finding exists.
func reportWorkflowChecks(out io.Writer, findings []driftFinding, asJSON bool) error {
	blocking := 0
	for _, f := range findings {
		if f.Severity == "block" {
			blocking++
		}
	}
	if asJSON {
		b, _ := json.MarshalIndent(findings, "", "  ")
		fmt.Fprintln(out, string(b))
	} else {
		for _, f := range findings {
			fmt.Fprintf(out, "%-7s %-22s %-36s %s\n", f.Severity, f.Code, f.Artifact, f.Message)
		}
		fmt.Fprintln(out, "\n── workflow drift check ──")
		if blocking == 0 {
			fmt.Fprintln(out, "verdict: CLEAN — the defined process and the executable reality agree.")
		} else {
			fmt.Fprintf(out, "verdict: BLOCKED — %d blocking finding(s); fix the drift or correct the definition.\n", blocking)
		}
	}
	if blocking > 0 {
		return fmt.Errorf("workflow check: %d blocking finding(s)", blocking)
	}
	return nil
}
