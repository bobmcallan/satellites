// Client-side caveman headline generation for `satellites document|principle
// upload` (epic:always-context, sty_f68f9053).
//
// Generation runs on the operator's machine via `claude -p` — the same
// mechanism the reviewer gates use (status_transition) — so it needs no
// server-side LLM key. After a document/principle is upserted with no stored
// headline, a terse one-liner is generated from its body and patched back in.
// Idempotent: it only fills an empty headline, so a re-upload never
// regenerates. Fail-soft: a missing claude binary, an error, or empty output
// leaves the headline empty (the index simply shows "-").

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// headlineGenTimeout bounds one claude -p generation call.
const headlineGenTimeout = 60 * time.Second

// headlineMaxLen clamps the stored headline — the index is a one-line listing,
// and the prompt already asks for ~80 chars; this is a hard backstop.
const headlineMaxLen = 120

// headlineSystemPrompt is the caveman ruleset — the generative twin of the
// skill-review critique (terse, drift-free, prescriptive). Embedded like the
// other client-side claude -p prompts (evidenceReviewSystemPrompt). The
// document name + body arrive on stdin.
const headlineSystemPrompt = `You are the headline generator for one satellites document or principle. You receive, on stdin, the document's name and body (frontmatter already stripped).

Produce ONE caveman headline: a terse, keyword-dense line that tells an agent at a glance what this document is for and when to reach for it. This is the generative twin of the skill-review critique — apply the same discipline.

Rules:
- Output exactly one line. No newline, no trailing period, no surrounding quotes, no markdown.
- Caveman compression: drop articles and filler ("the", "a", "is", "this document"). Lead with the substance — a verb or a noun phrase, never a preamble like "This document describes...".
- Keep it under ~80 characters.
- Never emit a concrete substrate id (no sty_, doc_, proj_, wksp_ hex slug). Refer to things by role, not id.

Return only the bare line.`

// shouldFillHeadline reports whether the upload path should generate a headline
// for an upserted row: only a document/principle (type "document") whose stored
// headline is still empty. Skills are never processed, and an existing headline
// (operator frontmatter or a prior generation) is left untouched — keeping
// re-upload idempotent.
func shouldFillHeadline(docType, storedHeadline string) bool {
	return docType == "document" && strings.TrimSpace(storedHeadline) == ""
}

// firstHeadlineLine normalises a claude completion into a single stored line:
// the first non-empty line, stripped of wrapping quotes/backticks and clamped
// to headlineMaxLen.
func firstHeadlineLine(raw string) string {
	for _, ln := range strings.Split(raw, "\n") {
		s := strings.TrimSpace(ln)
		if s == "" {
			continue
		}
		s = strings.Trim(s, "`\"'")
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if len(s) > headlineMaxLen {
			s = strings.TrimSpace(s[:headlineMaxLen])
		}
		return s
	}
	return ""
}

// generateHeadline runs the best-effort claude -p pass over the document body.
// Any failure (no claude, timeout, error, empty output) returns "" so the
// caller leaves the headline empty rather than failing the upload.
func generateHeadline(ctx context.Context, claudeBin, name, body string) string {
	bin := strings.TrimSpace(claudeBin)
	if bin == "" {
		bin = strings.TrimSpace(os.Getenv("SATELLITES_CLAUDE_BIN"))
	}
	if bin == "" {
		bin = "claude"
	}
	cctx, cancel := context.WithTimeout(ctx, headlineGenTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, "-p", "--append-system-prompt", headlineSystemPrompt)
	cmd.Stdin = strings.NewReader("name: " + name + "\n\n" + body)
	cmd.Env = os.Environ()
	outBytes, err := cmd.Output()
	if err != nil {
		return ""
	}
	return firstHeadlineLine(string(outBytes))
}

// fillHeadlineIfEmpty inspects the upsert response and, for a headline-less
// document/principle, generates a caveman headline and patches it in with a
// second upsert (same body bytes → no new version, only the headline metadata
// is applied). All failures are reported but never abort the upload.
func fillHeadlineIfEmpty(ctx context.Context, out io.Writer, resp json.RawMessage, t documentTarget, claudeBin, configArg, userArg string) {
	var parsed struct {
		Document struct {
			Type     string `json:"type"`
			Headline string `json:"headline"`
		} `json:"document"`
	}
	if err := json.Unmarshal(resp, &parsed); err != nil {
		return
	}
	if !shouldFillHeadline(parsed.Document.Type, parsed.Document.Headline) {
		return
	}
	line := generateHeadline(ctx, claudeBin, t.Name, t.Body)
	if line == "" {
		return
	}
	patched := t
	patched.Headline = line
	req, err := marshalUpsertRequest(patched)
	if err != nil {
		return
	}
	if _, err := dispatchVerb(ctx, "document_upsert", req, configArg, userArg); err != nil {
		fmt.Fprintf(out, "  headline: generation succeeded but patch failed: %v\n", err)
		return
	}
	fmt.Fprintf(out, "  headline: %s\n", line)
}
