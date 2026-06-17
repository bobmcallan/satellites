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
	"unicode/utf8"

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

// epicObjectiveCeiling bounds the parent-epic objective pushed alongside the
// re-anchor. It is a Purpose paragraph (small); this caps a runaway parent body
// so the lightweight re-anchor never bloats the child's context.
const epicObjectiveCeiling = 2048

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
	// the resident principles as story content fills the context, and push the
	// parent epic's objective alongside it so the child keeps the epic's "why"
	// in view. Only for a story fetch in a configured repo; otherwise silent.
	if len(ev.ToolInput) > 0 {
		storyID := storyIDFromToolInput(ev.ToolInput)
		if storyID == "" {
			return nil
		}
		root, ok := findSatellitesRepoRoot(repoStart(ev.Cwd))
		if !ok {
			return nil
		}
		configPath := filepath.Join(root, ".satellites", "satellites.toml")
		msg := alwaysReanchor
		if obj := epicObjectiveForStory(ctx, storyID, configPath, ""); obj != "" {
			msg = msg + "\n\n" + obj
		}
		return emitAdditionalContext(out, "PreToolUse", msg)
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
	// The resident set is the UNION of the SYSTEM baseline principles:always
	// (agent-goals, broken-windows, story-execution-process — and the
	// constitution once it is baselined) and THIS project's always-principles,
	// project winning on a name clash (a repo override of a baseline). Querying
	// project scope alone silently dropped every system baseline principle
	// (sty_957a041b). The project list is required (its failure fails the
	// inject); a system-list failure is non-fatal so a repo still gets its own
	// resident set.
	projItems, err := listAlwaysItems(ctx, "project", wsID, pjID, configPath, userID)
	if err != nil {
		return "", false, err
	}
	sysItems, sysErr := listAlwaysItems(ctx, "system", "", "", configPath, userID)
	if sysErr != nil {
		sysItems = nil
	}
	items := mergeAlwaysItems(sysItems, projItems)

	parts := make([]string, 0, len(items))
	for _, it := range items {
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

// listAlwaysItems lists the principles:always documents at one scope. System
// scope is global (no project binding); project scope needs the workspace +
// project binding the caller resolved. Effective overlays the user's own
// overrides onto the listed set.
func listAlwaysItems(ctx context.Context, scope, wsID, pjID, configPath, userID string) ([]alwaysListItem, error) {
	req := docListRequest{
		Type:      "document",
		Scope:     scope,
		Tags:      []string{alwaysTag},
		Limit:     50,
		Effective: true,
	}
	if scope == "project" {
		req.WorkspaceID = wsID
		req.ProjectID = pjID
	}
	listReq, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	raw, err := dispatchVerb(ctx, "document_list", listReq, configPath, userID)
	if err != nil {
		return nil, err
	}
	var listed struct {
		Items []alwaysListItem `json:"items"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		return nil, err
	}
	return listed.Items, nil
}

// mergeAlwaysItems unions the system-scope and project-scope always sets,
// project winning on a name clash (a repo override of a baseline principle).
// Order is stable: system baseline first, then project-only additions — a
// clashing project item replaces the system one in place. Both scopes inject;
// the resident set is the union, never project-only (sty_957a041b).
func mergeAlwaysItems(system, project []alwaysListItem) []alwaysListItem {
	out := make([]alwaysListItem, 0, len(system)+len(project))
	idx := map[string]int{}
	add := func(items []alwaysListItem) {
		for _, it := range items {
			if i, ok := idx[it.Name]; ok {
				out[i] = it // later scope (project) wins on a name clash
				continue
			}
			idx[it.Name] = len(out)
			out = append(out, it)
		}
	}
	add(system)
	add(project)
	return out
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

// storyDoc is the slice of a story row the epic-objective re-anchor needs.
type storyDoc struct {
	Name     string
	ParentID string
	Body     string
}

// fetchStoryDoc resolves a story by id via document_get. It is a package var so
// the re-anchor's formatting can be exercised in unit tests without a live
// server (the dispatch round-trip is the only un-hermetic part). Any failure is
// reported as (zero, false) so the caller fails open.
var fetchStoryDoc = func(ctx context.Context, id, configPath, userID string) (storyDoc, bool) {
	req, err := json.Marshal(struct {
		ID string `json:"id"`
	}{ID: id})
	if err != nil {
		return storyDoc{}, false
	}
	raw, err := dispatchVerb(ctx, "document_get", req, configPath, userID)
	if err != nil {
		return storyDoc{}, false
	}
	var got struct {
		Document struct {
			Name     string `json:"name"`
			ParentID string `json:"parent_id"`
		} `json:"document"`
		RenderedBody string `json:"rendered_body"`
		RawBody      string `json:"raw_body"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		return storyDoc{}, false
	}
	body := got.RenderedBody
	if strings.TrimSpace(body) == "" {
		body = got.RawBody
	}
	return storyDoc{Name: got.Document.Name, ParentID: got.Document.ParentID, Body: body}, true
}

// epicObjectiveForStory resolves the fetched story's immediate parent (the epic
// anchor) and returns its objective formatted for the re-anchor, or "" when the
// story has no parent or anything fails. One level only: a child sits directly
// under its epic in this substrate. Fails open — a missing/deleted parent or an
// unreachable server yields "", so the hook never errors the tool call.
func epicObjectiveForStory(ctx context.Context, storyID, configPath, userID string) string {
	child, ok := fetchStoryDoc(ctx, storyID, configPath, userID)
	if !ok || strings.TrimSpace(child.ParentID) == "" {
		return ""
	}
	parent, ok := fetchStoryDoc(ctx, child.ParentID, configPath, userID)
	if !ok {
		return ""
	}
	return formatEpicObjective(parent.Name, parent.Body)
}

// formatEpicObjective builds the labelled epic-objective block: the parent's
// title and its Purpose paragraph (the first body paragraph). Returns "" when
// there is nothing to show. An oversize purpose is truncated with a visible
// marker — never a silent partial.
func formatEpicObjective(title, body string) string {
	title = strings.TrimSpace(title)
	purpose := firstParagraph(body)
	if title == "" && purpose == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Epic objective (satellites)\n\n")
	b.WriteString("The story you are working serves this epic — keep its objective in view:\n\n")
	if title != "" {
		b.WriteString("**" + title + "**")
		if purpose != "" {
			b.WriteString("\n\n")
		}
	}
	if purpose != "" {
		b.WriteString(truncateExplicit(purpose, epicObjectiveCeiling))
	}
	return b.String()
}

// firstParagraph returns the first non-empty, non-heading, non-fence block of a
// markdown body — the convention's Purpose paragraph. Returns "" if none.
func firstParagraph(body string) string {
	for _, blk := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n\n") {
		t := strings.TrimSpace(blk)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "```") {
			continue
		}
		return t
	}
	return ""
}

// truncateExplicit caps s to ceiling bytes, appending a visible marker when it
// cuts — so a truncation is never a silent partial. UTF-8 safe: it never splits
// a rune.
func truncateExplicit(s string, ceiling int) string {
	if len(s) <= ceiling {
		return s
	}
	const marker = " … [epic objective truncated]"
	cut := ceiling - len(marker)
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimRight(s[:cut], " \n") + marker
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
