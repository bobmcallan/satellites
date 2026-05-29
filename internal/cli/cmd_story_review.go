// `satellites story review <story-id>` — the client-side reviewer gate
// (sty_ffec5dab). The gate is intrinsically client-side: it reads the
// workflow skill from the local worktree and runs `claude -p --skill`
// against the tree under review. The substrate (story, project-config,
// ledger, status) stays server-side and is reached through the existing
// document_get / document_upsert / ledger_* verbs — no new server verb.
//
// This is the sibling of `story run` (the executor driver). Where `run`
// spawns claude to DO the work, `review` spawns the gate skill to JUDGE
// the transition, then advances the status on accept.
//
// Single-source reuse: transition selection is owned by
// internal/workflow (Workflow.PickTransition); project-config parsing
// and the gate subprocess + decision parse are owned by internal/verb
// (ParseProjectConfig, ClaudeCLIGateDispatcher). This command only
// orchestrates them, and stays behind internal/verb's request/response
// types per the CLI layering guard (no internal/document or
// internal/ledger imports).

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workflow"
	"github.com/spf13/cobra"
)

func newStoryReviewCmd(configArg, userArg *string) *cobra.Command {
	var (
		claudeBin    string
		worktreeRoot string
	)
	cmd := &cobra.Command{
		Use:   "review <story-id>",
		Short: "Run the workflow gate for a story's current → next transition, client-side",
		Long: `review runs the reviewer gate for one story on the operator machine.

It resolves the story and project-config via server verbs, reads the
workflow skill from the LOCAL worktree, picks the gated transition, and
runs the gate as ` + "`claude -p --skill <gate>`" + ` against the worktree.
On accept it advances the story status via document_upsert and records
review_accept + status_transition ledger rows; on reject it records the
rejection notes. The gate runs where the worktree and claude live — the
substrate stays on the server.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runReview(ctx, reviewOpts{
				StoryID:      strings.TrimSpace(args[0]),
				ConfigPath:   *configArg,
				UserArg:      *userArg,
				ClaudeBin:    claudeBin,
				WorktreeRoot: worktreeRoot,
				Stdout:       cmd.OutOrStdout(),
				Stderr:       cmd.ErrOrStderr(),
			})
		},
	}
	cmd.Flags().StringVar(&claudeBin, "claude-bin", "", "Path to the claude binary (defaults to $SATELLITES_CLAUDE_BIN or `claude` on PATH).")
	cmd.Flags().StringVar(&worktreeRoot, "worktree", "", "Worktree root the workflow skill + gate run against (default: current directory).")
	return cmd
}

type reviewOpts struct {
	StoryID      string
	ConfigPath   string
	UserArg      string
	ClaudeBin    string
	WorktreeRoot string
	Stdout       io.Writer
	Stderr       io.Writer
}

// reviewStory is the minimal projection the reviewer needs from a
// document_get response. Typed inline so the CLI does not name
// internal/document (layering guard).
type reviewStory struct {
	ID          string
	Type        string
	Status      string
	Category    string
	ProjectID   string
	WorkspaceID string
}

func runReview(ctx context.Context, opts reviewOpts) error {
	if opts.StoryID == "" {
		return fmt.Errorf("story id required")
	}

	// 1. Resolve the story (substrate read — server).
	story, body, err := reviewGetStory(ctx, opts)
	if err != nil {
		return err
	}
	storyType := strings.TrimSpace(story.Category)
	if storyType == "" {
		return fmt.Errorf("story %s has no category — workflow lookup needs a story type", story.ID)
	}

	// 2. project-config → workflow skill path for this story type (server read).
	skillPath, err := reviewWorkflowSkillPath(ctx, opts, story, storyType)
	if err != nil {
		return err
	}

	// 3. Read + parse the workflow skill from the LOCAL worktree.
	clean := filepath.Clean(skillPath)
	skillBytes, err := os.ReadFile(filepath.Join(opts.WorktreeRoot, clean))
	if err != nil {
		return fmt.Errorf("read workflow skill %q from worktree: %w", skillPath, err)
	}
	wf, err := workflow.Parse(skillBytes)
	if err != nil {
		return fmt.Errorf("parse workflow %q: %w", skillPath, err)
	}

	// 4. Pick the gated transition (owned by internal/workflow).
	transition, gateSkill, isDynamic, err := wf.PickTransition(story.Status)
	if err != nil {
		return err
	}

	// 5. Recent ledger for gate context (server read). Inlined with :=
	// inference so the CLI never names internal/ledger (layering guard).
	var recentResp verb.LedgerListResponse
	if llReq, mErr := json.Marshal(verb.LedgerListRequest{StoryID: story.ID}); mErr == nil {
		if raw, lErr := dispatchVerb(ctx, "ledger_list", llReq, opts.ConfigPath, opts.UserArg); lErr == nil {
			_ = json.Unmarshal(raw, &recentResp)
		}
	}
	recent := recentResp.Entries
	if len(recent) > 5 {
		recent = recent[len(recent)-5:]
	}

	// 6. Spine: the gate was requested.
	reviewAppendLedger(ctx, opts, story, "review_requested",
		fmt.Sprintf("gate %s: %s → %s", gateSkill, story.Status, transition.To),
		map[string]any{"gate": gateSkill, "from_status": story.Status, "to_status": transition.To, "dynamic": isDynamic})

	// 7. Run the gate locally (owned by internal/verb's dispatcher).
	disp := verb.ClaudeCLIGateDispatcher{BinaryPath: strings.TrimSpace(opts.ClaudeBin), DefaultTimeout: 5 * time.Minute}
	gateOut, err := disp.Dispatch(ctx, verb.GateInput{
		SkillName:    gateSkill,
		StoryID:      story.ID,
		StoryBody:    body,
		StoryStatus:  story.Status,
		NextStatus:   transition.To,
		Dynamic:      isDynamic,
		RecentLedger: recent,
		WorktreeRoot: opts.WorktreeRoot,
	})
	if err != nil {
		return fmt.Errorf("gate dispatch: %w", err)
	}
	fmt.Fprintf(opts.Stdout, "decision: %s\n", gateOut.Decision)
	if strings.TrimSpace(gateOut.Notes) != "" {
		fmt.Fprintf(opts.Stdout, "notes: %s\n", gateOut.Notes)
	}

	// 8. Apply the decision against the substrate (server writes).
	if gateOut.Decision == verb.GateDecisionReject {
		reviewAppendLedger(ctx, opts, story, verb.KindReviewReject, gateOut.Notes,
			map[string]any{"from_status": story.Status, "gate": gateSkill, "notes": gateOut.Notes})
		return nil
	}

	newStatus := transition.To
	if isDynamic {
		chosen := strings.TrimSpace(gateOut.NextStatus)
		if chosen == "" {
			return fmt.Errorf("dynamic gate returned empty next_status")
		}
		if _, ok := wf.FindTransition(story.Status, chosen); !ok {
			return fmt.Errorf("dynamic gate returned undeclared transition %s → %s", story.Status, chosen)
		}
		newStatus = chosen
	}
	patchReq, err := json.Marshal(map[string]any{"id": story.ID, "status": newStatus})
	if err != nil {
		return err
	}
	if _, err := dispatchVerb(ctx, "document_upsert", patchReq, opts.ConfigPath, opts.UserArg); err != nil {
		return fmt.Errorf("patch status → %s: %w", newStatus, err)
	}
	reviewAppendLedger(ctx, opts, story, verb.KindReviewAccept, gateOut.Notes,
		map[string]any{"from_status": story.Status, "to_status": newStatus, "gate": gateSkill, "notes": gateOut.Notes})
	reviewAppendLedger(ctx, opts, story, verb.KindStatusTransition,
		fmt.Sprintf("%s → %s", story.Status, newStatus),
		map[string]any{"from_status": story.Status, "to_status": newStatus})
	fmt.Fprintf(opts.Stdout, "status: %s → %s\n", story.Status, newStatus)
	return nil
}

func reviewGetStory(ctx context.Context, opts reviewOpts) (reviewStory, string, error) {
	req, err := json.Marshal(verb.DocumentGetRequest{ID: opts.StoryID})
	if err != nil {
		return reviewStory{}, "", err
	}
	raw, err := dispatchVerb(ctx, "document_get", req, opts.ConfigPath, opts.UserArg)
	if err != nil {
		return reviewStory{}, "", fmt.Errorf("resolve story: %w", err)
	}
	var resp verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return reviewStory{}, "", fmt.Errorf("decode story: %w", err)
	}
	if resp.Document.Type != "story" {
		return reviewStory{}, "", fmt.Errorf("document %s is type=%q; review requires a story", opts.StoryID, resp.Document.Type)
	}
	body := resp.RawBody
	if body == "" && len(resp.Versions) > 0 {
		body = resp.Versions[0].Body
	}
	return reviewStory{
		ID:          resp.Document.ID,
		Type:        resp.Document.Type,
		Status:      resp.Document.Status,
		Category:    resp.Document.Category,
		ProjectID:   resp.Document.ProjectID,
		WorkspaceID: resp.Document.WorkspaceID,
	}, body, nil
}

func reviewWorkflowSkillPath(ctx context.Context, opts reviewOpts, story reviewStory, storyType string) (string, error) {
	req, err := json.Marshal(verb.DocumentGetRequest{
		Scope:       "project",
		Name:        "project-config",
		WorkspaceID: story.WorkspaceID,
		ProjectID:   story.ProjectID,
	})
	if err != nil {
		return "", err
	}
	raw, err := dispatchVerb(ctx, "document_get", req, opts.ConfigPath, opts.UserArg)
	if err != nil {
		return "", fmt.Errorf("load project-config: %w", err)
	}
	var resp verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("decode project-config: %w", err)
	}
	body := resp.RawBody
	if body == "" && len(resp.Versions) > 0 {
		body = resp.Versions[0].Body
	}
	cfg, err := verb.ParseProjectConfig(body)
	if err != nil {
		return "", fmt.Errorf("project-config: %w", err)
	}
	st, ok := cfg.StoryTypes[storyType]
	if !ok || strings.TrimSpace(st.WorkflowSkill) == "" {
		return "", fmt.Errorf("project-config has no workflow_skill for story_type=%q", storyType)
	}
	return st.WorkflowSkill, nil
}

func reviewAppendLedger(ctx context.Context, opts reviewOpts, story reviewStory, kind, body string, payload map[string]any) {
	var pb json.RawMessage
	if len(payload) > 0 {
		if b, err := json.Marshal(payload); err == nil {
			pb = b
		}
	}
	req, err := json.Marshal(verb.LedgerAppendRequest{
		StoryID:     story.ID,
		ProjectID:   story.ProjectID,
		WorkspaceID: story.WorkspaceID,
		Kind:        kind,
		Body:        body,
		Payload:     pb,
	})
	if err != nil {
		return
	}
	if _, err := dispatchVerb(ctx, "ledger_append", req, opts.ConfigPath, opts.UserArg); err != nil {
		fmt.Fprintf(opts.Stderr, "ledger append %s failed: %v\n", kind, err)
	}
}
