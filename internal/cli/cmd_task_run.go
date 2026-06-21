// `satellites task run <id>` — the LOCAL task runner (epic:contained-task-skills,
// salvaged from the retired local-task-runner-claude-p). It executes a task with
// the operator's local `claude -p` — no server LLM key — honouring the
// contained-skill contract: the task's work-skill is resolved LOCAL-FIRST (a
// .claude/skills/<name> override) → else the SUBSTRATE copy injected BY VALUE
// (bound to the copy the entry gate validated, not a skill-name invocation).
//
// The runner drives the executor loop: open via satellites-task-upsert-review →
// run the work via claude -p (skill body as --append-system-prompt) → write the
// report to the task body → close via satellites-task-report-review. Status is
// only ever moved by the gates; the runner never patches it directly.
//
// Autonomous / unattended execution stays a deferred spike (see the runner
// story) — this is the manual, operator-triggered path only.

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

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/spf13/cobra"
)

// taskRunExecTimeout caps a single executor `claude -p` invocation.
const taskRunExecTimeout = 15 * time.Minute

// workSkillSource records where the runner resolved a task's work-skill — the
// provenance the runner reports so the run record shows which copy actually ran.
type workSkillSource struct {
	Name    string
	Origin  string // "local-override" | "substrate" | "inline"
	Scope   string // substrate scope (project/workspace/system), when Origin=="substrate"
	Version int
	Body    string
}

// skillFetcher resolves a work-skill from the substrate by name. Injectable so
// resolveWorkSkill's precedence is unit-testable without a live substrate.
type skillFetcher func(ctx context.Context, name string) (body, scope string, version int, err error)

// resolveWorkSkill applies the contained-skill resolution contract: LOCAL-FIRST
// (a .claude/skills/<name>/SKILL.md override) → else the SUBSTRATE copy injected
// BY VALUE. A local file is the only sanctioned divergence; otherwise the runner
// binds to the substrate copy the entry gate validated. Returns the body +
// provenance.
func resolveWorkSkill(ctx context.Context, worktree, name string, fetch skillFetcher) (workSkillSource, error) {
	src := workSkillSource{Name: name}
	local := filepath.Join(worktree, ".claude", "skills", name, "SKILL.md")
	if b, err := os.ReadFile(local); err == nil {
		src.Origin = "local-override"
		src.Body = string(b)
		return src, nil
	}
	body, scope, version, err := fetch(ctx, name)
	if err != nil {
		return src, fmt.Errorf("work skill %q does not resolve in the substrate, and no local .claude/skills/%s override is present: %w", name, name, err)
	}
	src.Origin = "substrate"
	src.Scope = scope
	src.Version = version
	src.Body = body
	return src, nil
}

// skillNameFromTags returns the first `skill:<name>` tag's name, or "" when the
// task carries no work-skill delegation (an inline task).
func skillNameFromTags(tags []string) string {
	for _, t := range tags {
		if n := strings.TrimPrefix(t, "skill:"); n != t {
			return strings.TrimSpace(n)
		}
	}
	return ""
}

func newTaskRunCmd(configArg, userArg *string) *cobra.Command {
	var (
		claudeBin    string
		worktreeRoot string
		model        string
		dryRun       bool
	)
	cmd := &cobra.Command{
		Use:   "run <task-id>",
		Short: "Execute a task locally via claude -p — resolve its work-skill (local-first → substrate by value), run the work, write the report, drive the gates",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runTaskRun(ctx, taskRunOpts{
				TaskID:       strings.TrimSpace(args[0]),
				ConfigPath:   *configArg,
				UserArg:      *userArg,
				ClaudeBin:    claudeBin,
				WorktreeRoot: worktreeRoot,
				Model:        model,
				DryRun:       dryRun,
				Stdout:       cmd.OutOrStdout(),
				Stderr:       cmd.ErrOrStderr(),
			})
		},
	}
	cmd.Flags().StringVar(&claudeBin, "claude-bin", "", "Path to the claude binary (defaults to $SATELLITES_CLAUDE_BIN or `claude` on PATH).")
	cmd.Flags().StringVar(&worktreeRoot, "worktree", "", "Worktree root the executor runs against (default: current directory).")
	cmd.Flags().StringVar(&model, "model", "", "Optional --model for the executor claude -p invocation.")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Resolve the task + work-skill and print the plan without opening the task or invoking claude -p.")
	return cmd
}

type taskRunOpts struct {
	TaskID       string
	ConfigPath   string
	UserArg      string
	ClaudeBin    string
	WorktreeRoot string
	Model        string
	DryRun       bool
	Stdout       io.Writer
	Stderr       io.Writer
}

func runTaskRun(ctx context.Context, o taskRunOpts) error {
	// 1. Load the task.
	getReq, err := json.Marshal(verb.DocumentGetRequest{ID: o.TaskID})
	if err != nil {
		return err
	}
	raw, err := dispatchVerb(ctx, "document_get", getReq, o.ConfigPath, o.UserArg)
	if err != nil {
		return fmt.Errorf("task run: resolve %s: %w", o.TaskID, err)
	}
	var got verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		return fmt.Errorf("task run: decode %s: %w", o.TaskID, err)
	}
	if got.Document.Type != taskType {
		return fmt.Errorf("task run: %s is type=%q, not a task", o.TaskID, got.Document.Type)
	}
	taskBody := got.RawBody
	if taskBody == "" && len(got.Versions) > 0 {
		taskBody = got.Versions[len(got.Versions)-1].Body
	}

	worktree := strings.TrimSpace(o.WorktreeRoot)
	if worktree == "" {
		if wd, err := os.Getwd(); err == nil {
			worktree = wd
		}
	}

	// 2. Resolve the work-skill (local-first → substrate), if the task delegates.
	var skill workSkillSource
	if name := skillNameFromTags(got.Document.Tags); name != "" {
		fetch := substrateSkillFetcher(o.ConfigPath, o.UserArg, got.Document.ProjectID, got.Document.WorkspaceID)
		skill, err = resolveWorkSkill(ctx, worktree, name, fetch)
		if err != nil {
			return fmt.Errorf("task run: %w", err)
		}
	} else {
		skill.Origin = "inline"
	}

	fmt.Fprintf(o.Stdout, "task:      %s (%s)\n", o.TaskID, got.Document.Status)
	switch skill.Origin {
	case "local-override":
		fmt.Fprintf(o.Stdout, "work-skill: %s — LOCAL OVERRIDE (.claude/skills/%s)\n", skill.Name, skill.Name)
	case "substrate":
		fmt.Fprintf(o.Stdout, "work-skill: %s — substrate (scope=%s, v%d), injected by value\n", skill.Name, skill.Scope, skill.Version)
	default:
		fmt.Fprintf(o.Stdout, "work-skill: (inline — work step is in the task body)\n")
	}

	if o.DryRun {
		fmt.Fprintln(o.Stdout, "dry-run: would open (satellites-task-upsert-review) → run claude -p → write report → close (satellites-task-report-review)")
		return nil
	}

	// 3. Open via the entry gate (ready→running, or complete→running re-run).
	if err := runReview(ctx, reviewOpts{
		StoryID:      o.TaskID,
		ConfigPath:   o.ConfigPath,
		UserArg:      o.UserArg,
		ClaudeBin:    o.ClaudeBin,
		WorktreeRoot: o.WorktreeRoot,
		Skill:        "satellites-task-upsert-review",
		Stdout:       o.Stderr, // gate chatter to stderr; keep stdout for the result
		Stderr:       o.Stderr,
	}); err != nil {
		return fmt.Errorf("task run: open gate: %w", err)
	}

	// 4. Execute the work via claude -p (work-skill body injected by value).
	report, err := runTaskExecutor(ctx, o, skill.Body, taskBody)
	if err != nil {
		return fmt.Errorf("task run: executor: %w", err)
	}

	// 5. Persist the report into the task body so the exit gate can read it.
	provenance := "inline work step"
	if skill.Origin == "local-override" {
		provenance = fmt.Sprintf("work-skill %s (LOCAL OVERRIDE)", skill.Name)
	} else if skill.Origin == "substrate" {
		provenance = fmt.Sprintf("work-skill %s (substrate scope=%s v%d, by value)", skill.Name, skill.Scope, skill.Version)
	}
	newBody := strings.TrimRight(taskBody, "\n") + fmt.Sprintf("\n\n## Report (task run — %s)\n\n%s\n", provenance, strings.TrimSpace(report))
	upReq, err := json.Marshal(verb.DocumentUpsertRequest{ID: o.TaskID, Body: newBody})
	if err != nil {
		return err
	}
	if _, err := dispatchVerb(ctx, "document_upsert", upReq, o.ConfigPath, o.UserArg); err != nil {
		return fmt.Errorf("task run: write report: %w", err)
	}

	// 6. Close via the exit gate (running→complete) — it judges the report.
	if err := runReview(ctx, reviewOpts{
		StoryID:      o.TaskID,
		ConfigPath:   o.ConfigPath,
		UserArg:      o.UserArg,
		ClaudeBin:    o.ClaudeBin,
		WorktreeRoot: o.WorktreeRoot,
		Skill:        "satellites-task-report-review",
		Stdout:       o.Stderr,
		Stderr:       o.Stderr,
	}); err != nil {
		return fmt.Errorf("task run: close gate: %w", err)
	}

	fmt.Fprintf(o.Stdout, "done: report written and task closed via satellites-task-report-review (%s)\n", provenance)
	return nil
}

// substrateSkillFetcher resolves a work-skill from the substrate by name —
// project scope first (the task's project), then resolver default — returning
// the body + scope + version for provenance.
func substrateSkillFetcher(configPath, userArg, projectID, workspaceID string) skillFetcher {
	return func(ctx context.Context, name string) (string, string, int, error) {
		try := func(scope string) (string, string, int, bool) {
			req, err := json.Marshal(verb.DocumentGetRequest{
				Name:        name,
				Scope:       scope,
				ProjectID:   projectID,
				WorkspaceID: workspaceID,
			})
			if err != nil {
				return "", "", 0, false
			}
			raw, err := dispatchVerb(ctx, "document_get", req, configPath, userArg)
			if err != nil {
				return "", "", 0, false
			}
			var resp verb.DocumentGetResponse
			if err := json.Unmarshal(raw, &resp); err != nil {
				return "", "", 0, false
			}
			body := resp.RawBody
			if body == "" && len(resp.Versions) > 0 {
				body = resp.Versions[len(resp.Versions)-1].Body
			}
			if strings.TrimSpace(body) == "" {
				return "", "", 0, false
			}
			return body, string(resp.Document.Scope), resp.Document.LatestVersion, true
		}
		if body, scope, ver, ok := try("project"); ok {
			return body, scope, ver, nil
		}
		if body, scope, ver, ok := try(""); ok {
			return body, scope, ver, nil
		}
		return "", "", 0, fmt.Errorf("not found in substrate (project/workspace/system)")
	}
}

// runTaskExecutor invokes the operator's local `claude -p` to perform the task's
// work. The work-skill body (when present) is injected by value via
// --append-system-prompt; the task body is the prompt on stdin. It returns the
// executor's report text. No server LLM key is involved — execution is the
// operator's local claude.
func runTaskExecutor(ctx context.Context, o taskRunOpts, skillBody, taskBody string) (string, error) {
	bin := strings.TrimSpace(o.ClaudeBin)
	if bin == "" {
		bin = strings.TrimSpace(os.Getenv("SATELLITES_CLAUDE_BIN"))
	}
	if bin == "" {
		bin = "claude"
	}

	args := []string{"-p", "--allowedTools", "Bash Read Write Edit Grep Glob"}
	if strings.TrimSpace(skillBody) != "" {
		args = append(args, "--append-system-prompt", skillBody)
	}
	if m := strings.TrimSpace(o.Model); m != "" {
		args = append(args, "--model", m)
	}

	prompt := taskExecutorPrompt(o.TaskID, taskBody)

	cctx, cancel := context.WithTimeout(ctx, taskRunExecTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, args...)
	cmd.Stdin = strings.NewReader(prompt)
	if w := strings.TrimSpace(o.WorktreeRoot); w != "" {
		cmd.Dir = w
	}
	cmd.Env = os.Environ()

	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("claude -p failed: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("claude -p failed: %w", err)
	}
	report := strings.TrimSpace(string(out))
	if report == "" {
		return "", fmt.Errorf("claude -p produced no report output")
	}
	return report, nil
}

func taskExecutorPrompt(taskID, taskBody string) string {
	var b strings.Builder
	b.WriteString("You are executing a satellites TASK as its work step. Perform the work the task describes")
	b.WriteString(" (follow the work-skill procedure injected into your system prompt, when present).\n\n")
	b.WriteString("Output ONLY a Markdown report of the result — a short section with your findings and a one-line verdict.")
	b.WriteString(" The runner persists your output as the task's report and the exit gate (satellites-task-report-review) reads it.")
	b.WriteString(" Do the real work; never fabricate a result. Never print a secret's value.\n\n")
	b.WriteString(fmt.Sprintf("TASK %s:\n\n", taskID))
	b.WriteString(taskBody)
	return b.String()
}
