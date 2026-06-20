// `satellites workflow upsert` + `satellites workflow validate` (sty_fc39ca77).
//
// upsert closes the last gap in the reviewers-only model: workflows were the one
// substrate type authored locally and executed with NO review or server storage.
// It walks .satellites/workflows/ and upserts each as a kind:workflow document
// through the EXISTING generic uploadKind path, so gating is config-resolved
// (reviewSkillForKind("workflows") → satellites-workflow-review) with no new
// hardcoded kind branch — exactly the config-over-code shape skills/principles
// already use (sty_ec049962).
//
// validate is the deterministic DRY-RUN (the gate's functional half): it resolves
// every reviewer_skill and [[wikilink]] a single workflow file references through
// the dispatcher's embed→local→server cascade, reporting any that resolve from no
// tier. It is what the upsert pre-filter and the work-init drive-time gate both
// call (reviewWorkflowRefs), surfaced as a standalone command so an author can
// check a workflow before upserting and so the behaviour is unit-testable.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/spf13/cobra"
)

// workflowRefResolvers builds the two resolution predicates reviewWorkflowRefs
// needs, bound to the live substrate: resolveSkill folds the dispatcher's
// embed→local→server gate resolution (so an embedded internal gate resolves with
// no server, and a server-stored reviewer resolves when reachable); resolveDoc
// resolves a document/principle by bare name through the same project → workspace
// → system cascade `document get` uses. Both fail closed (false) on any
// resolution error — callers that must not over-block when offline probe a
// known-resident reference first (see the work-init drive-time gate).
func workflowRefResolvers(ctx context.Context, configArg, userArg string) (resolveSkill, resolveDoc func(string) bool) {
	fetch := serverGateFetcher(configArg, userArg)
	resolveSkill = func(name string) bool {
		return verb.GateResolvable(ctx, fetch, ".", name)
	}

	pid, _ := projectIDFromConfig(configArg)
	wsID := ""
	if pid != "" {
		wsID, _ = resolveWorkspaceID(ctx, pid, configArg, userArg)
	}
	getOne := func(name, scope, ws, pj string) bool {
		req, err := json.Marshal(struct {
			Name        string `json:"name"`
			Scope       string `json:"scope,omitempty"`
			WorkspaceID string `json:"workspace_id,omitempty"`
			ProjectID   string `json:"project_id,omitempty"`
		}{Name: name, Scope: scope, WorkspaceID: ws, ProjectID: pj})
		if err != nil {
			return false
		}
		raw, err := dispatchVerb(ctx, "document_get", req, configArg, userArg)
		if err != nil {
			return false
		}
		var parsed docGetFullView
		if json.Unmarshal(raw, &parsed) != nil {
			return false
		}
		return strings.TrimSpace(parsed.RenderedBody) != ""
	}
	resolveDoc = func(name string) bool {
		if pid != "" && getOne(name, "project", wsID, pid) {
			return true
		}
		if wsID != "" && getOne(name, "workspace", wsID, "") {
			return true
		}
		return getOne(name, "system", "", "")
	}
	return resolveSkill, resolveDoc
}

// newWorkflowUpsertCmd walks .satellites/workflows/ and upserts each workflow as
// a kind:workflow document via the shared uploadKind path. The verb is `upsert`
// (consistent with document_upsert / workspace_upsert), --dry-run plans without
// writing, and the per-kind reviewer (satellites-workflow-review) gates the push.
func newWorkflowUpsertCmd(configArg, userArg *string) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "upsert",
		Short: "Upsert each .satellites/workflows/ file as a kind:workflow document (gated by satellites-workflow-review)",
		Long: `upsert stores this repo's workflows on the server so a fresh install governs
by the authored workflow instead of silently falling back to the embedded
default. It walks .satellites/workflows/*.md and upserts each as a kind:workflow
document through the same review-gated path skills and principles use; the
satellites-workflow-review reviewer must accept before any workflow is stored.
--dry-run prints the planned upserts without writing.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			out := cmd.OutOrStdout()
			projectID, err := projectIDFromConfig(*configArg)
			if err != nil {
				return err
			}
			return uploadKind(ctx, out, "workflows", *configArg, *userArg, projectID, dryRun)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print the planned upserts without writing")
	return cmd
}

// newWorkflowValidateCmd runs the deterministic reference dry-run over a single
// workflow file and exits non-zero when any reviewer_skill or [[wikilink]]
// resolves from no tier — the functional half of satellites-workflow-review,
// usable standalone before an upsert.
func newWorkflowValidateCmd(configArg, userArg *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate <path>",
		Short: "Resolve every reviewer_skill and [[wikilink]] a workflow references — exit 1 on any dangling reference",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			raw, err := os.ReadFile(args[0])
			if err != nil {
				return fmt.Errorf("workflow validate: %w", err)
			}
			resolveSkill, resolveDoc := workflowRefResolvers(ctx, *configArg, *userArg)
			findings := reviewWorkflowRefs(string(raw), resolveSkill, resolveDoc)
			return reportWorkflowRefs(cmd.OutOrStdout(), args[0], findings)
		},
	}
	return cmd
}

// reportWorkflowRefs prints the dry-run findings and returns a non-nil error
// (exit 1) when any reference is unresolved. Shared by `workflow validate` and
// reused in the same shape by the upsert pre-filter and the drive-time gate.
func reportWorkflowRefs(out io.Writer, label string, findings []reviewFinding) error {
	if len(findings) == 0 {
		fmt.Fprintf(out, "%s: every reviewer_skill and [[wikilink]] resolves (embed → local → server)\n", label)
		return nil
	}
	fmt.Fprintf(out, "%s — %d unresolved reference(s):\n", label, len(findings))
	for _, f := range findings {
		fmt.Fprintf(out, "  ✗ %s\n", f.String())
	}
	return fmt.Errorf("workflow validate: %d unresolved reference(s) — fix the workflow and re-run", len(findings))
}
