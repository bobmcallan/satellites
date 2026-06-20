// `satellites workflow design <story>` — design a story's ## Workflow from its
// requirement, in isolated context (epic:qa-observability, story:sty_fc0090c2).
// A claude -p subagent (like the reviewer gates) authors candidate workflows from
// only the requirement, the available skills, and the fail-closed-gate principle;
// the orch/exec presents them and the operator chooses; --apply writes the chosen
// ## Workflow into the story. Fail-closed: an invalid proposal is never applied,
// and when none validates the command exits non-zero.
//
// Each proposal is validated through the same checks order:3 uses
// (reviewContextConflicts → workflow.ParseBody + ValidateLifecycle + gate-skill
// resolution), so a proposal naming a non-existent reviewer_skill or with a
// degenerate lifecycle cannot be applied.
//
// No new MCP verb: a client command over a claude -p subagent + existing reads.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/frontmatter"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workflow"
	"github.com/spf13/cobra"
)

const workflowDesignSkill = "satellites-workflow-design"
const workflowDesignTimeout = 5 * time.Minute

type wfProposal struct {
	Rationale string `json:"rationale"`
	Workflow  string `json:"workflow"`
}

type wfDesignOutput struct {
	Recommended int          `json:"recommended"`
	Proposals   []wfProposal `json:"proposals"`
}

func newWorkflowCmd(configArg, userArg *string) *cobra.Command {
	var (
		asJSON    bool
		apply     int
		claudeBin string
	)
	design := &cobra.Command{
		Use:   "design <story-id>",
		Short: "Design a story's ## Workflow from its requirement (claude -p subagent)",
		Long: `design runs an isolated workflow-design subagent (claude -p) over a story's
requirement plus the available skills and the fail-closed-gate principle, and
proposes candidate ## Workflow state machines, each validated (lifecycle + gate-
skill resolution, the order:3 checks) and justified. It presents the proposals;
--apply N writes the chosen one into the story (fail-closed: an invalid proposal
is refused, and the command errors when none validates).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runWorkflowDesign(ctx, strings.TrimSpace(args[0]), apply, asJSON, claudeBin, *configArg, *userArg, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	design.Flags().BoolVar(&asJSON, "json", false, "Emit the proposals + validity as JSON.")
	design.Flags().IntVar(&apply, "apply", -1, "Write proposal N's ## Workflow into the story (0-based). Refused if N is invalid.")
	design.Flags().StringVar(&claudeBin, "claude-bin", "", "Path to the claude binary (defaults to $SATELLITES_CLAUDE_BIN or `claude`).")

	workflowCmd := &cobra.Command{
		Use:   "workflow",
		Short: "Workflow tooling (design a story's ## Workflow)",
	}
	workflowCmd.PersistentFlags().StringVar(configArg, "config", "", "Path to satellites.toml (overrides $SATELLITES_CONFIG / .satellites/satellites.toml walk-up).")
	workflowCmd.PersistentFlags().StringVar(userArg, "user", "", "Caller user id (overrides $SATELLITES_USER_ID).")
	workflowCmd.AddCommand(design)
	workflowCmd.AddCommand(newWorkflowCheckCmd(configArg, userArg))
	workflowCmd.AddCommand(newWorkflowShowCmd(configArg, userArg))
	workflowCmd.AddCommand(newWorkflowEmbedCmd(configArg, userArg))
	workflowCmd.AddCommand(newWorkflowUpsertCmd(configArg, userArg))
	workflowCmd.AddCommand(newWorkflowValidateCmd(configArg, userArg))
	return workflowCmd
}

func init() {
	var (
		configArg string
		userArg   string
	)
	register(newWorkflowCmd(&configArg, &userArg))
}

func runWorkflowDesign(ctx context.Context, storyID string, apply int, asJSON bool, claudeBin, configPath, userArg string, stdout, stderr io.Writer) error {
	if storyID == "" {
		return fmt.Errorf("story id required")
	}
	story, body, err := reviewGetStory(ctx, reviewOpts{StoryID: storyID, ConfigPath: configPath, UserArg: userArg})
	if err != nil {
		return err
	}

	// Assemble the ISOLATED design context — requirement only (## Workflow
	// stripped), the available skills, and the fail-closed-gate principle.
	skillExists := func(name string) bool {
		_, e := os.Stat(filepath.Join(".claude", "skills", name, "SKILL.md"))
		return e == nil
	}
	designContext := buildDesignContext(replaceSection(body, "## Workflow", ""), configPath)

	skillBody, err := skillBodyOf(ctx, serverGateFetcher(configPath, userArg), workflowDesignSkill)
	if err != nil {
		return fmt.Errorf("read %s skill (run `satellites skill upload && satellites skill sync`?): %w", workflowDesignSkill, err)
	}
	raw, err := dispatchClaudeDesign(ctx, claudeBin, skillBody, designContext, stderr)
	if err != nil {
		return fmt.Errorf("workflow-design agent: %w", err)
	}
	out, err := parseDesignOutput(raw)
	if err != nil {
		return fmt.Errorf("workflow-design agent: %w", err)
	}
	if len(out.Proposals) == 0 {
		return fmt.Errorf("workflow-design agent returned no proposals")
	}

	// Validate each proposal through the order:3 checks.
	type judged struct {
		wfProposal
		Findings []conflictFinding `json:"findings"`
		Valid    bool              `json:"valid"`
	}
	js := make([]judged, len(out.Proposals))
	for i, p := range out.Proposals {
		f := reviewContextConflicts(p.Workflow, skillExists)
		js[i] = judged{wfProposal: p, Findings: f, Valid: len(f) == 0}
	}

	// --apply path: fail-closed.
	if apply >= 0 {
		if apply >= len(js) {
			return fmt.Errorf("--apply %d out of range (have %d proposals)", apply, len(js))
		}
		if !js[apply].Valid {
			return fmt.Errorf("--apply %d refused: proposal is invalid (%d finding(s)); fix the design or choose another", apply, len(js[apply].Findings))
		}
		newBody := replaceSection(body, "## Workflow", "## Workflow\n\n"+strings.TrimSpace(js[apply].Workflow)+"\n")
		if err := applyStoryBody(ctx, story, newBody, configPath, userArg); err != nil {
			return fmt.Errorf("write ## Workflow into story: %w", err)
		}
		fmt.Fprintf(stdout, "applied proposal %d to %s — ## Workflow written.\n", apply, storyID)
		return nil
	}

	// Presentation (no --apply). Fail-closed: error if nothing validated.
	anyValid := false
	for _, j := range js {
		if j.Valid {
			anyValid = true
		}
	}
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{"story_id": storyID, "recommended": out.Recommended, "proposals": js})
	} else {
		fmt.Fprintf(stdout, "workflow design for %s — %d proposal(s) (recommended: %d)\n\n", storyID, len(js), out.Recommended)
		for i, j := range js {
			mark := "✓ valid"
			if !j.Valid {
				mark = fmt.Sprintf("✗ invalid (%d finding(s))", len(j.Findings))
			}
			star := " "
			if i == out.Recommended {
				star = "*"
			}
			fmt.Fprintf(stdout, "%s [%d] %s — %s\n", star, i, mark, j.Rationale)
			for _, f := range j.Findings {
				fmt.Fprintf(stdout, "      - %s: %s\n", f.Code, f.Message)
			}
			fmt.Fprintf(stdout, "%s\n", indent(strings.TrimSpace(j.Workflow), "      "))
		}
		fmt.Fprintf(stdout, "\napply: satellites workflow design %s --apply <N>\n", storyID)
	}
	if !anyValid {
		return fmt.Errorf("no proposal passed validation — absent a compliant design the story cannot proceed (fail closed)")
	}
	return nil
}

// buildDesignContext marshals the isolated inputs the design agent receives.
func buildDesignContext(requirement, configPath string) string {
	payload := map[string]any{
		"requirement":                requirement,
		"available_workflow_skills":  availableWorkflowSkills(configPath),
		"available_gate_skills":      availableGateSkills(),
		"fail_closed_gate_principle": failClosedGatePrinciple(),
	}
	b, _ := json.Marshal(payload)
	return string(b)
}

// availableWorkflowSkills returns each available workflow's name + its workflow
// yaml — client-dir workflows (.satellites/workflows) and materialised
// kind:workflow skills — so the design agent can reuse a canonical shape when it
// fits.
func availableWorkflowSkills(configPath string) []map[string]string {
	var out []map[string]string
	for _, s := range append(clientWorkflows(configPath), materialisedSkills()...) {
		if s.kind != "workflow" {
			continue
		}
		yaml, err := workflow.ExtractYAMLBlock([]byte(s.body))
		if err != nil {
			continue
		}
		out = append(out, map[string]string{"name": s.name, "workflow": "```yaml\n" + string(yaml) + "\n```"})
	}
	return out
}

// availableGateSkills returns the reviewer-gate skills (name + description) the
// design agent may reference as reviewer_skill. Selection is by the declared
// dispatch contract (frontmatter kind: gate), not the name — a capability that
// happens to be named *-review (skill-review, document-review, …) is not a
// gate and must not be offered, while every true gate (techdebt, doc-drift)
// is offered regardless of naming (sty_c3f126cb atomicity).
func availableGateSkills() []map[string]string {
	var out []map[string]string
	for _, s := range materialisedSkills() {
		if !isReviewerSkillKind(s.kind) {
			continue
		}
		out = append(out, map[string]string{"name": s.name, "description": s.description})
	}
	return out
}

type matSkill struct {
	name        string
	kind        string
	scope       string // frontmatter scope ("" = project/unset)
	description string
	body        string
	raw         string // full on-disk file content (stamp + frontmatter + body)
	// local marks a skill AUTHORED in this repo's skill authoring dir
	// (repo-owned), as opposed to one INHERITED by sync from a publisher/system
	// (a palette). Populated by markLocalAuthorship on the workflow-check path
	// only; the orphan-gate rule uses it (sty_f8f88f92).
	local bool
}

// materialisedWorkflowSources projects the materialised kind:workflow skills
// into the verb resolver's input — the registry's governing-workflow candidates
// for a story's category (sty_0889de7a). The .claude/skills set is already
// scope-cascade-resolved by sync, so resolving over it honours the cascade.
func materialisedWorkflowSources() []verb.WorkflowSource {
	var out []verb.WorkflowSource
	for _, s := range materialisedSkills() {
		if s.kind == "workflow" {
			out = append(out, verb.WorkflowSource{Name: s.name, Body: s.raw})
		}
	}
	return out
}

// materialisedSkills reads the .claude/skills set (name, kind, description, body).
func materialisedSkills() []matSkill {
	var out []matSkill
	entries, err := os.ReadDir(filepath.Join(".claude", "skills"))
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		raw, rerr := os.ReadFile(filepath.Join(".claude", "skills", e.Name(), "SKILL.md"))
		if rerr != nil {
			continue
		}
		stripped := frontmatter.StripSyncStamp(raw)
		fm, bodyB, perr := frontmatter.Parse(stripped)
		if perr != nil {
			continue
		}
		name := strings.TrimSpace(fm.Name)
		if name == "" {
			name = e.Name()
		}
		out = append(out, matSkill{name: name, kind: strings.TrimSpace(fm.Kind), scope: strings.TrimSpace(fm.Scope), description: strings.TrimSpace(fm.Description), body: string(bodyB), raw: string(raw)})
	}
	return out
}

// failClosedGatePrinciple reads the fail-closed-gate principle body — from the
// repo-local source if present (it may not be uploaded yet), best-effort.
func failClosedGatePrinciple() string {
	raw, err := os.ReadFile(filepath.Join(resolveSubstrateRoot("principles", ""), "principles", "fail-closed-gate.md"))
	if err != nil {
		return ""
	}
	_, body, err := frontmatter.Parse(raw)
	if err != nil {
		return string(raw)
	}
	return string(body)
}

// skillBodyOf returns a substrate skill's body (frontmatter + sync-stamp
// stripped), resolving local materialised → server (sty_b8de4776): on a local
// miss it fetches the body by name from the server through the same
// scope-precedence resolution gate dispatch uses, so a capability skill absent
// from .claude/skills still resolves for claude -p (no local install needed).
func skillBodyOf(ctx context.Context, fetch verb.GateBodyFetcher, name string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(".claude", "skills", name, "SKILL.md"))
	if err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}
		if fetch == nil {
			return "", err
		}
		fetched, ok, ferr := fetch(ctx, name)
		if ferr != nil {
			return "", fmt.Errorf("fetch skill %q from server: %w", name, ferr)
		}
		if !ok {
			return "", fmt.Errorf("skill %q resolves from no source (not in .claude/skills, not on the server)", name)
		}
		raw = fetched
	}
	_, body, err := frontmatter.Parse(frontmatter.StripSyncStamp(raw))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// dispatchClaudeDesign runs the design subagent: `claude -p` with the design
// skill as the system prompt and the isolated context on stdin.
func dispatchClaudeDesign(ctx context.Context, claudeBin, skillBody, designContext string, stderr io.Writer) (string, error) {
	bin := strings.TrimSpace(claudeBin)
	if bin == "" {
		bin = strings.TrimSpace(os.Getenv("SATELLITES_CLAUDE_BIN"))
	}
	if bin == "" {
		bin = "claude"
	}
	cctx, cancel := context.WithTimeout(ctx, workflowDesignTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, "-p", "--allowedTools", "Read Grep Glob", "--append-system-prompt", skillBody)
	cmd.Stdin = strings.NewReader(designContext)
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("claude -p: %w", err)
	}
	return string(out), nil
}

// parseDesignOutput tolerantly extracts the design JSON from the agent's stdout
// (strips code fences + surrounding prose, takes the outermost object).
func parseDesignOutput(raw string) (wfDesignOutput, error) {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "```"); i >= 0 {
		// drop a leading fence line if present
		if nl := strings.IndexByte(s[i:], '\n'); nl >= 0 {
			rest := s[i+nl+1:]
			if end := strings.LastIndex(rest, "```"); end >= 0 {
				s = rest[:end]
			}
		}
	}
	lo := strings.IndexByte(s, '{')
	hi := strings.LastIndexByte(s, '}')
	if lo < 0 || hi <= lo {
		return wfDesignOutput{}, fmt.Errorf("no JSON object in agent output (raw=%.200q)", raw)
	}
	var out wfDesignOutput
	if err := json.Unmarshal([]byte(s[lo:hi+1]), &out); err != nil {
		return wfDesignOutput{}, fmt.Errorf("parse agent JSON: %w", err)
	}
	return out, nil
}

// applyStoryBody writes a new story body via document_upsert (patch by id),
// preserving everything but the rewritten ## Workflow section.
func applyStoryBody(ctx context.Context, story reviewStory, newBody, configPath, userArg string) error {
	req, err := json.Marshal(verb.DocumentUpsertRequest{ID: story.ID, Body: newBody})
	if err != nil {
		return err
	}
	_, err = dispatchVerb(ctx, "document_upsert", req, configPath, userArg)
	return err
}

// replaceSection replaces the markdown section beginning at heading (up to the
// next `## ` heading or EOF) with replacement. If the heading is absent, the
// replacement is appended. Pure.
func replaceSection(body, heading, replacement string) string {
	lines := strings.Split(body, "\n")
	start := -1
	for i, ln := range lines {
		if strings.TrimSpace(ln) == heading {
			start = i
			break
		}
	}
	if start < 0 {
		trimmed := strings.TrimRight(body, "\n")
		if strings.TrimSpace(replacement) == "" {
			return trimmed + "\n"
		}
		return trimmed + "\n\n" + strings.TrimSpace(replacement) + "\n"
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}
	head := strings.Join(lines[:start], "\n")
	tail := strings.Join(lines[end:], "\n")
	var b strings.Builder
	b.WriteString(strings.TrimRight(head, "\n"))
	if strings.TrimSpace(replacement) != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(strings.TrimSpace(replacement))
	}
	if strings.TrimSpace(tail) != "" {
		b.WriteString("\n\n")
		b.WriteString(strings.TrimLeft(tail, "\n"))
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// indent prefixes every line of s with pad.
func indent(s, pad string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = pad + ln
	}
	return strings.Join(lines, "\n")
}
