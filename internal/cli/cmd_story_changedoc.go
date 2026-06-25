// `satellites story changedoc` — generate a story's PR-like CHANGE DOCUMENT and
// attach it as a typed, story-linked `type:summary` project document (sty_201dc6c6).
//
//	satellites story changedoc <sty> [--summary <md> | --summary-file <path>]
//
// The change document is the third default datapoint a story records at done
// (alongside the estimate and actual of sty_9643a847): a concise, PR-shaped
// record carrying
//   - the GIT RECORD — the commits that reference the story and the files they
//     changed (`git log --grep <sty>`);
//   - the ACTUAL WORKFLOW as a YAML projection AND a mermaid flowchart, both
//     rendered from the reconciled processtrace (declared-vs-actual), so the
//     traversal — including reject loops — reads at a glance; mermaid renders
//     natively in the portal markdown view.
//
// It is built-in MECHANISM, not a per-story hand-authored doc: the executor may
// pass a short narrative (the "why", like a PR description) and the command
// deterministically appends the git record and the actual-workflow projections,
// then attaches the result via the same document_upsert + ledger_append chain
// `story output` uses. The default done gate (satellites-story-done-review)
// requires this attached change document.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/bobmcallan/satellites/internal/processtrace"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workflow"
	"github.com/spf13/cobra"
)

type storyChangedocOpts struct {
	Name        string
	Summary     string
	SummaryFile string
}

func newStoryChangedocCmd(configArg, userArg *string) *cobra.Command {
	var o storyChangedocOpts
	cmd := &cobra.Command{
		Use:   "changedoc <story-id> [--summary <md> | --summary-file <path>]",
		Short: "Generate and attach a story's PR-like change document (git record + actual-workflow YAML + mermaid)",
		Long: `changedoc assembles a concise, PR-shaped change document for a story and
attaches it as a story-linked type:summary project document. It composes a short
narrative you supply (--summary / --summary-file, optional) with a deterministic
git record (commits referencing the story + files changed) and the actual
workflow rendered as YAML and a mermaid flowchart from the reconciled
processtrace. Run it at the done boundary, after the story's commit has landed,
then request the done gate — which requires the attached change document.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runStoryChangedoc(ctx, cmd.OutOrStdout(), *configArg, *userArg, strings.TrimSpace(args[0]), o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.Name, "name", "", "Document name (default: \"<story-id> change document\")")
	f.StringVar(&o.Summary, "summary", "", "Short narrative (the \"why\", PR-description style)")
	f.StringVar(&o.SummaryFile, "summary-file", "", "Path to a file whose contents become the narrative")
	return cmd
}

func runStoryChangedoc(ctx context.Context, out io.Writer, configPath, userArg, storyID string, o storyChangedocOpts) error {
	// Resolve the story: confirm it IS a story and capture its project + body.
	getReq, _ := json.Marshal(verb.DocumentGetRequest{ID: storyID})
	raw, err := dispatchVerb(ctx, "document_get", getReq, configPath, userArg)
	if err != nil {
		return fmt.Errorf("story changedoc: resolve %s: %w", storyID, err)
	}
	var storyResp verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &storyResp); err != nil {
		return fmt.Errorf("story changedoc: decode %s: %w", storyID, err)
	}
	if storyResp.Document.Type != storyType {
		return fmt.Errorf("story changedoc: %s is type=%q, not a story", storyID, storyResp.Document.Type)
	}
	projectID := storyResp.Document.ProjectID
	if strings.TrimSpace(projectID) == "" {
		return fmt.Errorf("story changedoc: story %s has no project_id", storyID)
	}
	body := storyResp.RawBody

	narrative := o.Summary
	if strings.TrimSpace(o.SummaryFile) != "" {
		b, rErr := os.ReadFile(o.SummaryFile)
		if rErr != nil {
			return fmt.Errorf("story changedoc: read summary file: %w", rErr)
		}
		narrative = string(b)
	}

	// Reconcile the actual workflow: ledger + the story's embedded ## Workflow
	// (or its category default), exactly as the portal trace view does.
	entries, lErr := dispatchStoryLedgerEntries(ctx, storyID, configPath, userArg)
	if lErr != nil {
		fmt.Fprintf(out, "warning: ledger unavailable, workflow projection omitted: %v\n", lErr)
	}
	wf := resolveStoryWorkflow(configPath, body, storyResp.Document.Category)

	name := strings.TrimSpace(o.Name)
	if name == "" {
		name = storyID + " change document"
	}
	docBody := assembleChangedoc(storyID, storyResp.Document.Name, narrative, gitStoryRecord(storyID), entries, storyResp.Document.Category, storyResp.Document.Status, wf)

	// Attach as a story-linked type:summary document, reusing the story-output
	// tag set and link row so it is traceable and the done gate can find it.
	tags := buildStoryOutputTags("summary", "", "", storyID)
	upReq, _ := json.Marshal(verb.DocumentUpsertRequest{
		Type:        "document",
		Scope:       "project",
		ProjectID:   projectID,
		WorkspaceID: storyResp.Document.WorkspaceID,
		Name:        name,
		Body:        docBody,
		Tags:        &tags,
	})
	raw, err = dispatchVerb(ctx, "document_upsert", upReq, configPath, userArg)
	if err != nil {
		return fmt.Errorf("story changedoc: create change document: %w", err)
	}
	var upResp verb.DocumentUpsertResponse
	if err := json.Unmarshal(raw, &upResp); err != nil {
		return fmt.Errorf("story changedoc: decode created document: %w", err)
	}
	outputID := upResp.Document.ID

	payload, _ := json.Marshal(storyOutputLedgerPayload{OutputID: outputID, Name: name, Kind: "summary"})
	logReq, _ := json.Marshal(verb.LedgerAppendRequest{StoryID: storyID, ProjectID: projectID, Kind: storyOutputLedgerKind, Payload: payload})
	if _, err := dispatchVerb(ctx, "ledger_append", logReq, configPath, userArg); err != nil {
		fmt.Fprintf(out, "warning: change document created but story link failed: %v\n", err)
	}

	fmt.Fprintf(out, "%s  %s  [%s]\n", outputID, name, strings.Join(tags, ", "))
	return nil
}

// gatherStageFacts builds the deterministic facts blob the stage reviewer is
// fed (sty_201dc6c6): the story's git record and, when a workflow is embedded,
// the reconciled actual-workflow YAML + mermaid from processtrace. The markdown
// reviewer owns judgment + generation; this only supplies faithful structured
// context so the reviewer never has to invent the diagram or the diff list.
func gatherStageFacts(ctx context.Context, configPath, userArg, storyID, body, category, status string) string {
	var b strings.Builder
	git := gitStoryRecord(storyID)
	b.WriteString("## git record\n")
	if len(git.Commits) == 0 {
		b.WriteString("(no commits reference this story yet)\n")
	} else {
		for _, c := range git.Commits {
			b.WriteString("commit: " + c + "\n")
		}
		for _, f := range git.Files {
			b.WriteString("file: " + f + "\n")
		}
	}
	wf := resolveStoryWorkflow(configPath, body, category)
	if wf != nil {
		entries, _ := dispatchStoryLedgerEntries(ctx, storyID, configPath, userArg)
		pt := processtrace.Reconcile(storyID, category, status, wf, entries, nil)
		if y, err := processtrace.ActualWorkflow(pt).YAML(); err == nil {
			b.WriteString("\n## actual workflow (yaml)\n" + y)
		}
		b.WriteString("\n## actual workflow (mermaid)\n" + processtrace.MermaidActualWorkflow(pt))
	}
	return b.String()
}

// dispatchStoryLedgerEntries lists a story's ledger and maps it into the
// processtrace projection — the CLI peer of the server's dispatchStoryLedger.
func dispatchStoryLedgerEntries(ctx context.Context, storyID, configPath, userArg string) ([]processtrace.LedgerEntry, error) {
	req, _ := json.Marshal(verb.LedgerListRequest{StoryID: storyID, Limit: 500})
	raw, err := dispatchVerb(ctx, "ledger_list", req, configPath, userArg)
	if err != nil {
		return nil, err
	}
	var resp verb.LedgerListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	out := make([]processtrace.LedgerEntry, 0, len(resp.Entries))
	for _, e := range resp.Entries {
		out = append(out, processtrace.LedgerEntry{Kind: e.Kind, Body: e.Body, Payload: e.Payload, Actor: e.Actor, CreatedAt: e.CreatedAt})
	}
	return out, nil
}

// gitRecord is the commits + files a story touched.
type gitRecord struct {
	Commits []string // "<short-hash> <subject>"
	Files   []string // unique changed paths
}

// gitStoryRecord recovers the story's git record via `git log --grep <sty>`
// (commits carry the (sty_<id>) reference in their subject). Best-effort: any
// git error yields an empty record, never a failure — a story may have no
// commit yet.
func gitStoryRecord(storyID string) gitRecord {
	var r gitRecord
	if out, err := exec.Command("git", "log", "--grep="+storyID, "--format=%h %s").Output(); err == nil {
		for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if ln = strings.TrimSpace(ln); ln != "" {
				r.Commits = append(r.Commits, ln)
			}
		}
	}
	if out, err := exec.Command("git", "log", "--grep="+storyID, "--name-only", "--format=").Output(); err == nil {
		seen := map[string]bool{}
		for _, ln := range strings.Split(string(out), "\n") {
			if ln = strings.TrimSpace(ln); ln != "" && !seen[ln] {
				seen[ln] = true
				r.Files = append(r.Files, ln)
			}
		}
	}
	return r
}

// assembleChangedoc renders the PR-like change document markdown: an optional
// narrative, the git record, and the actual-workflow YAML + mermaid projections.
// resolveStoryWorkflow resolves the story's governing workflow for the
// actual-workflow projection (sty_e61afcf5): the embedded `## Workflow` in the
// body wins when present, else it falls back to the same governing-workflow
// resolution the gates use (by category/selector), so a story whose body lacks
// the embed (e.g. it was rewritten via document_upsert) still gets a faithful
// projection rather than an omitted one. Returns nil when neither resolves.
func resolveStoryWorkflow(configPath, body, category string) *workflow.Workflow {
	if wf, err := workflow.ParseBody([]byte(body)); err == nil && wf != nil {
		return wf
	}
	if wf, _, ok := verb.ResolveGoverningWorkflow(category, governingWorkflowSources(configPath)); ok {
		return wf
	}
	return nil
}

func assembleChangedoc(storyID, title, narrative string, git gitRecord, entries []processtrace.LedgerEntry, category, status string, wf *workflow.Workflow) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — change document\n\n", strings.TrimSpace(title))
	fmt.Fprintf(&b, "`%s` · status %s\n\n", storyID, status)

	if n := strings.TrimSpace(narrative); n != "" {
		b.WriteString(n)
		b.WriteString("\n\n")
	}

	b.WriteString("## Git record\n\n")
	if len(git.Commits) == 0 {
		fmt.Fprintf(&b, "_No commits reference %s yet._\n\n", storyID)
	} else {
		b.WriteString("Commits:\n\n")
		for _, c := range git.Commits {
			fmt.Fprintf(&b, "- %s\n", c)
		}
		b.WriteString("\nFiles changed:\n\n")
		for _, f := range git.Files {
			fmt.Fprintf(&b, "- `%s`\n", f)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Actual workflow\n\n")
	if wf == nil {
		b.WriteString("_No governing workflow embedded — workflow projection omitted._\n")
		return b.String()
	}
	pt := processtrace.Reconcile(storyID, category, status, wf, entries, nil)
	if y, err := processtrace.ActualWorkflow(pt).YAML(); err == nil {
		b.WriteString("```yaml\n")
		b.WriteString(y)
		if !strings.HasSuffix(y, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("```\n\n")
	}
	b.WriteString("```mermaid\n")
	b.WriteString(processtrace.MermaidActualWorkflow(pt))
	b.WriteString("```\n")
	return b.String()
}
