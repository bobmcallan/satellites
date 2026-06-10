// `satellites hook context` (SessionStart) — the always-context injector
// (epic:always-context, sty_a29e1845).
//
// At session start it fetches every `always`-flagged document/principle (the
// principles:always residency marker) and injects their bodies as session
// context, followed by the standing instruction to pull the rest on demand via
// `satellites document index`. This keeps the small resident set in front of
// the agent without auto-injecting an unbounded list — the bodies are bounded
// by a byte ceiling, and an overflow is reported on stderr (never silently
// dropped). Fails open: an unconfigured repo or a fetch error injects nothing
// and never blocks the session.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// alwaysContextCeiling bounds the total injected always-content. The resident
// set is meant to be small (each always doc is principle-sized, well under 2k);
// this is the backstop that stops a mis-tagged large doc from blowing the
// context budget the whole model is meant to protect.
const alwaysContextCeiling = 8192

// alwaysIndexInstruction is the standing "pull, don't preload" directive
// appended to every injection — the pivot of the always-context model: the
// resident set is pushed, everything else is discovered on demand.
const alwaysIndexInstruction = "To discover other documents and principles beyond the resident set above, run `satellites document index` and load only the ones the task needs — do not preload everything."

// alwaysReanchor is the lightweight pointer re-injected when a story is fetched
// (the access hook), so an agent deep in an epic is reminded the resident
// principles still apply without re-streaming their bodies on every fetch.
const alwaysReanchor = "satellites: the always-resident principles injected at session start remain in effect. Re-read them if unsure, and run `satellites document index` to discover any other document/principle this work needs."

// newHookContextCmd builds the SessionStart always-context injector.
func newHookContextCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "context",
		Short: "SessionStart always-context injector — inject always-flagged docs/principles + the index pointer",
		Long: `context is the SessionStart handler. It fetches every always-flagged
(principles:always) document/principle and injects their bodies as session
context, then the standing instruction to discover the rest via
` + "`satellites document index`" + `. Bounded by a byte ceiling (overflow noted on
stderr); fails open so it never blocks a session.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHookContext(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
}

// contextHookInput is the slice of the event the injector reads. It serves two
// hook sites: SessionStart (no tool_input) injects the full resident set;
// PreToolUse on a story document_get (tool_input carries the story id)
// re-anchors with a lightweight pointer. Branching on tool_input presence keeps
// it robust without depending on a hook_event_name field.
type contextHookInput struct {
	SessionID string          `json:"session_id"`
	Cwd       string          `json:"cwd"`
	ToolInput json.RawMessage `json:"tool_input"`
}

func runHookContext(ctx context.Context, in io.Reader, out, stderr io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	raw, _ := io.ReadAll(in)
	var ev contextHookInput
	_ = json.Unmarshal(raw, &ev) // tolerate empty/garbage — fail open

	// PreToolUse path: a story document_get. Re-anchor with the lightweight
	// pointer (not the bodies) so an agent deep in an epic does not drift off
	// the resident principles as story content fills the context. Only for a
	// story fetch in a configured repo; otherwise silent.
	if len(ev.ToolInput) > 0 {
		if storyIDFromToolInput(ev.ToolInput) == "" {
			return nil
		}
		if _, ok := findSatellitesRepoRoot(repoStart(ev.Cwd)); !ok {
			return nil
		}
		return emitAdditionalContext(out, "PreToolUse", alwaysReanchor)
	}

	// SessionStart path: inject the full resident set + the index pointer.
	// Resolve the substrate config from the EVENT cwd's repo root (not the
	// process cwd), so the right project is queried and an unconfigured repo
	// fails open.
	root, ok := findSatellitesRepoRoot(repoStart(ev.Cwd))
	if !ok {
		return nil
	}
	configPath := filepath.Join(root, ".satellites", "satellites.toml")
	content, truncated, err := buildAlwaysContent(ctx, configPath, "")
	if err != nil {
		// Fail open: an unconfigured repo / unreachable server injects nothing.
		return nil
	}
	if truncated {
		fmt.Fprintf(stderr,
			"satellites hook context: always-content exceeded %d bytes and was truncated — trim an always-tagged doc or drop its principles:always tag\n",
			alwaysContextCeiling)
	}
	if strings.TrimSpace(content) == "" {
		return nil
	}
	return emitAdditionalContext(out, "SessionStart", content)
}

// repoStart resolves the directory to begin the repo-root walk from: the event
// cwd when present, else the process working directory.
func repoStart(cwd string) string {
	if s := strings.TrimSpace(cwd); s != "" {
		return s
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

// alwaysListItem is the slice of a document_list row the injector needs.
type alwaysListItem struct {
	Name  string `json:"name"`
	Scope string `json:"scope"`
}

// buildAlwaysContent fetches the always-flagged set and assembles the bounded
// injection text + the standing index instruction. Returns (content,
// truncated, err). A list/get failure is returned as err so the caller can fail
// open. The standing instruction is always present, even when no always docs
// exist yet, so the pull-the-index discipline is taught from day one.
func buildAlwaysContent(ctx context.Context, configPath, userID string) (string, bool, error) {
	// Project scope needs the project (+ workspace) binding — resolve it from
	// the config the same way `satellites document index` does, else the server
	// refuses the list with "project scope requires project_id".
	wsID, pjID := resolveSkillScopeBinding(ctx, configPath, userID, "", "")
	listReq, err := json.Marshal(docListRequest{
		Type:        "document",
		Scope:       "project",
		WorkspaceID: wsID,
		ProjectID:   pjID,
		Tags:        []string{alwaysTag},
		Limit:       50,
		Effective:   true,
	})
	if err != nil {
		return "", false, err
	}
	raw, err := dispatchVerb(ctx, "document_list", listReq, configPath, userID)
	if err != nil {
		return "", false, err
	}
	var listed struct {
		Items []alwaysListItem `json:"items"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		return "", false, err
	}

	parts := make([]string, 0, len(listed.Items))
	for _, it := range listed.Items {
		body, gerr := fetchDocumentBody(ctx, it.Name, it.Scope, wsID, pjID, configPath, userID)
		if gerr != nil || strings.TrimSpace(body) == "" {
			continue // skip an unreadable member rather than abort the whole inject
		}
		parts = append(parts, fmt.Sprintf("### %s\n\n%s", it.Name, strings.TrimSpace(body)))
	}

	joined, truncated := boundAlwaysParts(parts, alwaysContextCeiling)
	var b strings.Builder
	if joined != "" {
		b.WriteString("# Always-resident principles (satellites)\n\n")
		b.WriteString(joined)
		b.WriteString("\n\n")
	}
	b.WriteString(alwaysIndexInstruction)
	return b.String(), truncated, nil
}

// fetchDocumentBody pulls one document's rendered body via document_get. A
// project/workspace-scoped member needs the binding too; a system member
// ignores it.
func fetchDocumentBody(ctx context.Context, name, scope, wsID, pjID, configPath, userID string) (string, error) {
	req := struct {
		Name        string `json:"name"`
		Scope       string `json:"scope,omitempty"`
		WorkspaceID string `json:"workspace_id,omitempty"`
		ProjectID   string `json:"project_id,omitempty"`
	}{Name: name, Scope: scope}
	if scope != "system" {
		req.WorkspaceID = wsID
		req.ProjectID = pjID
	}
	getReq, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	raw, err := dispatchVerb(ctx, "document_get", getReq, configPath, userID)
	if err != nil {
		return "", err
	}
	var got struct {
		RenderedBody string `json:"rendered_body"`
		RawBody      string `json:"raw_body"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		return "", err
	}
	if strings.TrimSpace(got.RenderedBody) != "" {
		return got.RenderedBody, nil
	}
	return got.RawBody, nil
}

// boundAlwaysParts joins parts under a byte ceiling. It adds whole parts until
// the next one would exceed the ceiling, then stops and reports truncation —
// never a partial part, never silent. Returns the joined text + whether any
// part was dropped.
func boundAlwaysParts(parts []string, ceiling int) (string, bool) {
	var b strings.Builder
	truncated := false
	for i, p := range parts {
		sep := 0
		if b.Len() > 0 {
			sep = 2 // the "\n\n" joiner
		}
		if b.Len()+sep+len(p) > ceiling {
			truncated = true
			break
		}
		if i > 0 && b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(p)
	}
	return b.String(), truncated
}
