// sty_de9f10f9 — assert that the templated story-row in
// _panel_stories.html and the JS-built row in
// pages/static/common.js:_appendStoryRow render the SAME ordered
// list of <td class="…"> cells. The col-select drift that landed
// silently after sty_43d72112 / sty_b211e1f6 is exactly the kind
// of thing this test would have caught on the WS-append path.
//
// Both sides are derived from source on every run, NOT from
// snapshots — so future drift fails loud without a snapshot
// refresh ritual.
//
// Today the two sides DIVERGE: SSR has a leading col-select cell
// the JS builder omits. The test records that *known divergence*
// and asserts the diff equals the recorded set. When the sibling
// story closes the gap (the JS adds col-select OR the SSR drops
// it), this test will fail with "drift no longer matches recorded
// set" — that's the signal to tighten the assertion to strict
// equality. See `expectedKnownDivergence_storyRow` below.
package portal

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/pages"
)

// expectedKnownDivergence_storyRow records the cells SSR has but JS
// does not (and vice-versa) AS OF sty_de9f10f9 landing. When the
// sibling story closes the col-select drift, this set will become
// empty and the test will fail at the equality check below — that
// failure is the breadcrumb to flip this test to strict equality.
//
// TODO sty_<sibling-refactor>: when SSR/JS row builders are
// unified, change this whole test to assert ssrCells == jsCells
// and delete the divergence list.
var expectedKnownDivergence_storyRow = struct {
	OnlyInSSR []string // cells the SSR template renders that JS doesn't
	OnlyInJS  []string // cells JS builds that SSR doesn't
}{
	OnlyInSSR: []string{"col-select"},
	OnlyInJS:  nil,
}

func TestStoryRow_SSR_JS_ColumnParity(t *testing.T) {
	t.Parallel()

	ssrCells := renderStoryRowSSRCells(t)
	jsCells := extractAppendStoryRowJSCells(t)

	onlyInSSR := setDiff(ssrCells, jsCells)
	onlyInJS := setDiff(jsCells, ssrCells)

	if !equalStringSlices(onlyInSSR, expectedKnownDivergence_storyRow.OnlyInSSR) ||
		!equalStringSlices(onlyInJS, expectedKnownDivergence_storyRow.OnlyInJS) {
		t.Fatalf(
			"story-row SSR/JS column drift no longer matches the recorded set.\n"+
				"  ssr cells: %v\n"+
				"  js  cells: %v\n"+
				"  only in SSR: %v (expected %v)\n"+
				"  only in JS:  %v (expected %v)\n"+
				"this test was written xfail-style to track a known drift\n"+
				"(sty_de9f10f9). when the divergence changes, update\n"+
				"expectedKnownDivergence_storyRow OR — better — flip the\n"+
				"assertion to strict equality and delete the divergence list.",
			ssrCells, jsCells, onlyInSSR, expectedKnownDivergence_storyRow.OnlyInSSR,
			onlyInJS, expectedKnownDivergence_storyRow.OnlyInJS,
		)
	}
}

// renderStoryRowSSRCells executes the real _panel_stories.html
// template via the same pages.Templates() entrypoint the portal
// uses, with one synthesised storyCard so `range .Composite.Stories`
// produces exactly one tr.story-row. Returns the ordered class
// attribute of every <td> inside that row (NOT the detail row).
func renderStoryRowSSRCells(t *testing.T) []string {
	t.Helper()
	tmpl, err := pages.Templates()
	if err != nil {
		t.Fatalf("pages.Templates: %v", err)
	}

	composite := projectWorkspaceComposite{
		Stories: []storyCard{{
			ID:                 "sty_parity",
			ProjectID:          "proj_parity",
			Title:              "parity-probe",
			Status:             "backlog",
			Priority:           "high",
			Category:           "feature",
			Tags:               []string{"area:parity"},
			CreatedAt:          "2026-01-01T00:00:00Z",
			UpdatedAt:          "2026-01-01T00:00:00Z",
			Description:        "probe",
			AcceptanceCriteria: "probe",
		}},
	}
	data := map[string]any{"Composite": composite}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "_panel_stories.html", data); err != nil {
		t.Fatalf("execute _panel_stories.html: %v", err)
	}

	rendered := buf.String()
	rowOpen := strings.Index(rendered, `<tr class="story-row"`)
	if rowOpen < 0 {
		t.Fatalf("rendered template has no <tr class=\"story-row\">; rendered=%s", rendered)
	}
	rowClose := strings.Index(rendered[rowOpen:], "</tr>")
	if rowClose < 0 {
		t.Fatalf("rendered story-row has no </tr>")
	}
	rowSlice := rendered[rowOpen : rowOpen+rowClose]
	return scanTDClasses(rowSlice)
}

// extractAppendStoryRowJSCells reads pages/static/common.js, locates
// the _appendStoryRow function body, and scans the row.innerHTML
// concatenation for ordered <td class="…"> literals.
func extractAppendStoryRowJSCells(t *testing.T) []string {
	t.Helper()
	source := readCommonJS(t)
	body := extractJSFunctionBody(t, source, "_appendStoryRow(")
	// Restrict to the row.innerHTML assignment. The detail row
	// builder uses detail.innerHTML which we explicitly want to
	// skip — the SSR comparison is row-only.
	rowInner := isolateInnerHTMLAssignment(t, body, "row.innerHTML")
	return scanTDClasses(rowInner)
}

// scanTDClasses returns the ordered class attribute of every
// <td class="…"> literal in the haystack. Both rendered HTML and
// the JS source use plain `class="…"` (the JS strings are single-
// quoted), so a single regex covers both sides.
func scanTDClasses(haystack string) []string {
	re := regexp.MustCompile(`<td\s+class="([^"]+)"`)
	matches := re.FindAllStringSubmatch(haystack, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, normaliseClassAttr(m[1]))
	}
	return out
}

// normaliseClassAttr keeps only the first space-separated token —
// the column-identifying class. Avoids tripping on cells that pile
// modifiers like `col-updated muted`.
func normaliseClassAttr(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexAny(s, " \t"); idx > 0 {
		return s[:idx]
	}
	return s
}

// extractJSFunctionBody finds the method-shorthand DEFINITION of
// the named function (lines like `<name>(args) {`) — NOT a call
// site like `this.<name>(...)`. Returns everything from the opening
// `{` to its matched closing `}`. Robust to nested braces.
func extractJSFunctionBody(t *testing.T, source, name string) string {
	t.Helper()
	// Match a definition: start-of-line (after optional whitespace),
	// the bare name, the opening paren, the args, the closing paren,
	// optional whitespace, then `{`. The leading anchor stops us
	// from picking up `this.<name>(` call sites.
	pat := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(strings.TrimSuffix(name, "(")) + `\([^)]*\)\s*{`)
	loc := pat.FindStringIndex(source)
	if loc == nil {
		t.Fatalf("function definition for %q not found in common.js", name)
	}
	start := loc[1] - 1 // index of the opening `{`
	depth := 0
	for i := start; i < len(source); i++ {
		switch source[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return source[start : i+1]
			}
		}
	}
	t.Fatalf("matched closing brace not found for %q", name)
	return ""
}

// isolateInnerHTMLAssignment returns the substring of body
// containing the assignment to <target> = '…' + '…' + … ; up to
// the terminating semicolon. Subsequent .innerHTML assignments are
// excluded so the story-row scan doesn't pick up detail-row cells.
func isolateInnerHTMLAssignment(t *testing.T, body, target string) string {
	t.Helper()
	idx := strings.Index(body, target)
	if idx < 0 {
		t.Fatalf("%s assignment not found in function body", target)
	}
	end := strings.Index(body[idx:], ";")
	if end < 0 {
		t.Fatalf("%s assignment has no terminating semicolon", target)
	}
	return body[idx : idx+end]
}

// readCommonJS reads pages/static/common.js relative to the repo
// root (walking up from the test file's package path).
func readCommonJS(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// internal/portal → repo root is two parents up.
	root := filepath.Clean(filepath.Join(wd, "..", ".."))
	path := filepath.Join(root, "pages", "static", "common.js")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read common.js (%s): %v", path, err)
	}
	return string(raw)
}

// setDiff returns elements of a not present in b, preserving a's
// order then de-duped + sorted for stable comparison.
func setDiff(a, b []string) []string {
	if len(a) == 0 {
		return nil
	}
	bSet := make(map[string]struct{}, len(b))
	for _, v := range b {
		bSet[v] = struct{}{}
	}
	seen := make(map[string]struct{}, len(a))
	out := make([]string, 0)
	for _, v := range a {
		if _, ok := bSet[v]; ok {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}

// equalStringSlices compares two slices treating nil and empty as
// equal. Order-insensitive (callers pass already-sorted slices).
func equalStringSlices(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
