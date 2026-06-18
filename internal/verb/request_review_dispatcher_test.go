package verb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGateClaudeArgs pins the claude argv a gate run constructs. The
// `--skill` flag does not exist in the claude CLI; sty_1312d692 replaced
// it with `--append-system-prompt`. sty_cba1d47b added `--allowedTools` so
// the gate can actually build and run tests in the worktree instead of
// false-accepting on a static read. This test fails if `--skill` ever
// returns, if the system prompt is dropped, or if the tool grant goes
// missing — catching the class of bug that shipped a gate that could not
// verify yet accepted anyway.
func TestGateClaudeArgs(t *testing.T) {
	args := gateClaudeArgs("GATE BODY", "")
	want := []string{"-p", "--allowedTools", gateAllowedTools, "--append-system-prompt", "GATE BODY"}
	if len(args) != len(want) {
		t.Fatalf("argv length = %d, want %d (%v)", len(args), len(want), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("argv[%d] = %q, want %q (full %v)", i, args[i], want[i], args)
		}
	}
	for _, a := range args {
		if a == "--skill" {
			t.Fatalf("argv must not contain the non-existent --skill flag: %v", args)
		}
	}

	// The gate must be granted Bash (go build/test, git, satellites CLI)
	// plus the file-read tools — without these it cannot run the
	// verification its rubric demands (sty_cba1d47b AC1).
	pos := -1
	for i, a := range args {
		if a == "--allowedTools" {
			pos = i
			break
		}
	}
	if pos < 0 || pos+1 >= len(args) {
		t.Fatalf("argv must grant tool access via --allowedTools: %v", args)
	}
	grant := args[pos+1]
	for _, tool := range []string{"Bash", "Read", "Grep", "Glob"} {
		if !strings.Contains(grant, tool) {
			t.Fatalf("--allowedTools grant %q missing required tool %q", grant, tool)
		}
	}
}

// TestResolveGateSkillBody reads .claude/skills/<name>/SKILL.md from the
// worktree and returns the body with frontmatter stripped.
func TestResolveGateSkillBody(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, ".claude", "skills", "done-review")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "---\nname: done-review\ntype: skill\ntags: [kind:gate]\n---\nYou are the done-review gate. Emit decision JSON.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	body, err := resolveGateSkillBody(root, "done-review")
	if err != nil {
		t.Fatalf("resolveGateSkillBody: %v", err)
	}
	if strings.Contains(body, "---") || strings.Contains(body, "name: done-review") {
		t.Fatalf("frontmatter not stripped from gate body: %q", body)
	}
	if !strings.Contains(body, "You are the done-review gate") {
		t.Fatalf("gate body missing rubric: %q", body)
	}
}

// TestResolveGateSkillBody_Missing surfaces a clear error when the gate
// skill is absent from the worktree rather than dispatching an empty
// system prompt.
func TestResolveGateSkillBody_Missing(t *testing.T) {
	_, err := resolveGateSkillBody(t.TempDir(), "nonexistent-gate")
	if err == nil || !strings.Contains(err.Error(), "read gate skill") {
		t.Fatalf("expected read-gate-skill error, got %v", err)
	}
}

// substrateGateBody is a minimal materialised gate SKILL.md (frontmatter +
// body) the resolution tests stand in for either a worktree copy or a server
// fetch. No `check:` so Dispatch runs no functional half.
func substrateGateBody(name, marker string) string {
	return "---\nname: " + name + "\ntype: skill\nkind: gate\ntags: [kind:gate]\n---\n" + marker + "\n"
}

// writeLocalGate materialises a gate SKILL.md under root/.claude/skills/<name>.
func writeLocalGate(t *testing.T, root, name, marker string) {
	t.Helper()
	dir := filepath.Join(root, ".claude", "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(substrateGateBody(name, marker)), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestResolveGate_EmbedWins pins the home-of-gate invariant in the resolution
// chain (sty_b8de4776 AC5): a satellites-INTERNAL gate resolves from the binary
// FIRST and never consults the worktree or the server, even when a same-named
// local copy exists and a fetcher is wired. The fetcher fails the test if called.
func TestResolveGate_EmbedWins(t *testing.T) {
	root := t.TempDir()
	writeLocalGate(t, root, "satellites-intent-plan-review", "LOCAL IMPOSTOR BODY")
	disp := ClaudeCLIGateDispatcher{Fetch: func(_ context.Context, name string) ([]byte, bool, error) {
		t.Fatalf("server fetch must not be consulted for embedded gate %q", name)
		return nil, false, nil
	}}
	_, body, err := disp.resolveGate(context.Background(), root, "satellites-intent-plan-review")
	if err != nil {
		t.Fatalf("resolveGate: %v", err)
	}
	if strings.Contains(body, "IMPOSTOR") {
		t.Fatalf("embedded gate must win over local copy; got worktree body: %q", body)
	}
	if !strings.Contains(body, "satellites-formatted") {
		t.Fatalf("expected embedded intent-plan-review rubric, got: %q", body)
	}
}

// TestResolveGate_LocalHit: a non-embedded gate present in the worktree resolves
// from the local materialised dir — the FIRST non-embedded source (offline
// cache) — and the server fetcher is NOT consulted (no extra network when cached).
func TestResolveGate_LocalHit(t *testing.T) {
	root := t.TempDir()
	writeLocalGate(t, root, "custom-review", "LOCAL CACHE BODY")
	disp := ClaudeCLIGateDispatcher{Fetch: func(_ context.Context, name string) ([]byte, bool, error) {
		t.Fatalf("server fetch must not be consulted when local copy present (gate %q)", name)
		return nil, false, nil
	}}
	_, body, err := disp.resolveGate(context.Background(), root, "custom-review")
	if err != nil {
		t.Fatalf("resolveGate: %v", err)
	}
	if !strings.Contains(body, "LOCAL CACHE BODY") {
		t.Fatalf("expected local cache body, got: %q", body)
	}
}

// TestResolveGate_ServerFallback: a non-embedded gate ABSENT from the worktree
// is fetched from the server and injected — the core sty_b8de4776 property (no
// local install required). The worktree has no .claude/skills entry for it.
func TestResolveGate_ServerFallback(t *testing.T) {
	root := t.TempDir()
	var asked string
	disp := ClaudeCLIGateDispatcher{Fetch: func(_ context.Context, name string) ([]byte, bool, error) {
		asked = name
		return []byte(substrateGateBody(name, "SERVER FETCHED BODY")), true, nil
	}}
	_, body, err := disp.resolveGate(context.Background(), root, "custom-review")
	if err != nil {
		t.Fatalf("resolveGate: %v", err)
	}
	if asked != "custom-review" {
		t.Fatalf("server fetch asked for %q, want custom-review", asked)
	}
	if !strings.Contains(body, "SERVER FETCHED BODY") {
		t.Fatalf("expected server-fetched body, got: %q", body)
	}
}

// TestResolveGate_Nowhere: a gate that resolves from NO source — not embedded,
// not local, server returns ok=false — is a clear fail-closed dispatch error
// naming all three sources (sty_b8de4776 AC2).
func TestResolveGate_Nowhere(t *testing.T) {
	disp := ClaudeCLIGateDispatcher{Fetch: func(_ context.Context, _ string) ([]byte, bool, error) {
		return nil, false, nil // server has no such skill in any scope
	}}
	_, _, err := disp.resolveGate(context.Background(), t.TempDir(), "ghost-review")
	if err == nil {
		t.Fatalf("expected fail-closed error when gate resolves nowhere")
	}
	for _, want := range []string{"embedded", ".claude/skills", "server"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("nowhere error must name source %q; got %v", want, err)
		}
	}
}

// TestResolveGate_FetchErrorNotMasked: a server transport failure is returned as
// an error, never silently read as a cache miss / accept.
func TestResolveGate_FetchErrorNotMasked(t *testing.T) {
	disp := ClaudeCLIGateDispatcher{Fetch: func(_ context.Context, _ string) ([]byte, bool, error) {
		return nil, false, errors.New("substrate unreachable")
	}}
	_, _, err := disp.resolveGate(context.Background(), t.TempDir(), "custom-review")
	if err == nil || !strings.Contains(err.Error(), "substrate unreachable") {
		t.Fatalf("transport failure must surface, got %v", err)
	}
}

// TestResolveGate_NoFetcherKeepsLocalError: with no fetcher wired, an absent
// non-embedded gate still fails closed with the local read error (the server
// step is simply disabled — embed → local only).
func TestResolveGate_NoFetcherKeepsLocalError(t *testing.T) {
	disp := ClaudeCLIGateDispatcher{} // Fetch nil
	_, _, err := disp.resolveGate(context.Background(), t.TempDir(), "custom-review")
	if err == nil || !strings.Contains(err.Error(), "read gate skill") {
		t.Fatalf("expected local read-gate-skill error with no fetcher, got %v", err)
	}
}

// TestDispatch_SucceedsViaServerFetch proves the end-to-end property (AC4): a
// reviewer ABSENT from the worktree dispatches successfully because its body is
// fetched from the server and injected into the claude run. A shim binary stands
// in for `claude -p`, printing the decision JSON a real gate would emit.
func TestDispatch_SucceedsViaServerFetch(t *testing.T) {
	dir := t.TempDir()
	shim := filepath.Join(dir, "claude-shim.sh")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\nprintf '%s' '{\"decision\":\"accept\",\"notes\":\"resolved via server\"}'\n"), 0o755); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	worktree := t.TempDir() // no .claude/skills — the reviewer is absent locally
	disp := ClaudeCLIGateDispatcher{
		BinaryPath: shim,
		Fetch: func(_ context.Context, name string) ([]byte, bool, error) {
			return []byte(substrateGateBody(name, "You are the custom-review gate.")), true, nil
		},
	}
	out, err := disp.Dispatch(context.Background(), GateInput{
		SkillName:    "custom-review",
		StoryID:      "sty_test",
		StoryStatus:  "in_progress",
		WorktreeRoot: worktree,
	})
	if err != nil {
		t.Fatalf("dispatch via server fetch failed: %v", err)
	}
	if out.Decision != GateDecisionAccept {
		t.Fatalf("decision = %q, want accept", out.Decision)
	}
}

// TestParseGateOutput_BraceBearingProse pins the sty_756ad5f3 fix: the gate
// emitted paragraphs of reasoning containing a `{skills,documents,...}`
// token before its decision JSON. The old parser locked onto that first `{`
// and threw the real verdict away on a parse error. The decision — and its
// notes — must survive prose that carries braces.
func TestParseGateOutput_BraceBearingProse(t *testing.T) {
	raw := []byte(`I reviewed the change. The skills live under
config/wksp_6f048cd8/proj_fc7d72d8/{skills,documents,principles}/ and
AC4 requires .claude/skills/ to be gitignored, which it is not.

{"decision": "reject", "notes": "AC4 unmet: .claude/skills/ is not gitignored"}
`)
	out, err := ParseGateOutput(raw)
	if err != nil {
		t.Fatalf("ParseGateOutput on brace-bearing prose: %v", err)
	}
	if out.Decision != GateDecisionReject {
		t.Fatalf("decision = %q, want %q", out.Decision, GateDecisionReject)
	}
	if !strings.Contains(out.Notes, "AC4 unmet") {
		t.Fatalf("notes lost: %q", out.Notes)
	}
}

// TestParseGateOutput_TrailingProseAfterDecision covers a brace appearing in
// prose *after* the decision too — taking the last valid decision object
// must not be confused by a later `{...}` that is not a verdict.
func TestParseGateOutput_TrailingProseAfterDecision(t *testing.T) {
	raw := []byte(`Reasoning first.
{"decision": "accept", "notes": "all ACs met"}
Note: see config/{a,b}/ for details.`)
	out, err := ParseGateOutput(raw)
	if err != nil {
		t.Fatalf("ParseGateOutput: %v", err)
	}
	if out.Decision != GateDecisionAccept {
		t.Fatalf("decision = %q, want %q", out.Decision, GateDecisionAccept)
	}
	if out.Notes != "all ACs met" {
		t.Fatalf("notes = %q, want %q", out.Notes, "all ACs met")
	}
}

// TestParseGateOutput_CleanJSON keeps the bare-object path working (AC4).
func TestParseGateOutput_CleanJSON(t *testing.T) {
	out, err := ParseGateOutput([]byte(`{"decision":"accept","notes":"ok"}`))
	if err != nil {
		t.Fatalf("ParseGateOutput on clean JSON: %v", err)
	}
	if out.Decision != GateDecisionAccept || out.Notes != "ok" {
		t.Fatalf("got %+v, want accept/ok", out)
	}
}

// TestParseGateOutput_CodeFenced keeps the ```json-fenced path working (AC4).
func TestParseGateOutput_CodeFenced(t *testing.T) {
	raw := []byte("```json\n{\"decision\":\"reject\",\"notes\":\"missing test\"}\n```")
	out, err := ParseGateOutput(raw)
	if err != nil {
		t.Fatalf("ParseGateOutput on code-fenced JSON: %v", err)
	}
	if out.Decision != GateDecisionReject || out.Notes != "missing test" {
		t.Fatalf("got %+v, want reject/missing test", out)
	}
}

// TestParseGateOutput_NoDecision errors rather than silently accepting when
// the output carries no valid decision object (AC3) — including the case
// where prose has braces but no decision JSON at all.
func TestParseGateOutput_NoDecision(t *testing.T) {
	cases := map[string][]byte{
		"prose only":          []byte("I could not reach a decision."),
		"brace prose only":    []byte("paths are config/{skills,documents}/ here"),
		"empty":               []byte(""),
		"object wrong field":  []byte(`{"verdict":"accept"}`),
		"decision out of set": []byte(`{"decision":"maybe","notes":"unsure"}`),
		"unbalanced brace":    []byte(`{"decision": "accept"`),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := ParseGateOutput(raw)
			if err == nil {
				t.Fatalf("expected error, got decision %q", out.Decision)
			}
			if out.Decision != "" {
				t.Fatalf("decision must be empty on error, got %q", out.Decision)
			}
		})
	}
}

// TestGateClaudeArgs_ReviewerModel pins the reviewer_model rider
// (sty_c7a5d741): a configured model appends `--model <value>`; unset leaves
// the argv byte-identical to the default (inherit the harness model).
func TestGateClaudeArgs_ReviewerModel(t *testing.T) {
	base := gateClaudeArgs("GATE BODY", "")
	withModel := gateClaudeArgs("GATE BODY", "claude-sonnet-4-6")
	if len(withModel) != len(base)+2 {
		t.Fatalf("model argv length = %d, want %d+2 (%v)", len(withModel), len(base), withModel)
	}
	if withModel[len(withModel)-2] != "--model" || withModel[len(withModel)-1] != "claude-sonnet-4-6" {
		t.Fatalf("model must ride as trailing --model <value>: %v", withModel)
	}
	for _, a := range base {
		if a == "--model" {
			t.Fatalf("unset model must add no --model flag: %v", base)
		}
	}
}
