// sty_de9f10f9 — same shape as storyrow_parity_test.go but for the
// task sub-table inside the expanded story-row. SSR side is the
// `<tr class="story-task-row">` rendered by the inner range over
// .Contracts; JS side is _appendContractRow in common.js.
//
// Today the two sides ARE aligned (4 cols: col-seq, col-name,
// col-status, col-agent). The test passes on day one and acts as a
// regression guard so any future drift fails immediately. NO
// xfail-style divergence list — this is a strict equality check.
package portal

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/pages"
)

func TestTaskRow_SSR_JS_ColumnParity(t *testing.T) {
	t.Parallel()

	ssrCells := renderTaskRowSSRCells(t)
	jsCells := extractAppendContractRowJSCells(t)

	if len(ssrCells) == 0 {
		t.Fatalf("SSR task-row rendered no cells; rendering plumbing broke")
	}
	if len(jsCells) == 0 {
		t.Fatalf("JS _appendContractRow has no <td class=…> literals; parser broke")
	}
	if !equalStringSlices(ssrCells, jsCells) {
		t.Fatalf("task-row SSR/JS column drift detected.\n"+
			"  ssr cells: %v\n"+
			"  js  cells: %v\n"+
			"if the drift was intentional, update both sides; otherwise\n"+
			"this is a real bug — the WS-appended row will render with a\n"+
			"different shape than the templated row.",
			ssrCells, jsCells)
	}
}

// renderTaskRowSSRCells executes _panel_stories.html with one
// story carrying one contract row, then extracts the ordered <td
// class=…> list of the resulting <tr class="story-task-row">.
func renderTaskRowSSRCells(t *testing.T) []string {
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
			Contracts: []storyContractCard{{
				ID:           "ci_parity",
				Sequence:     0,
				ContractName: "plan",
				Status:       "ready",
			}},
		}},
	}
	data := map[string]any{"Composite": composite}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "_panel_stories.html", data); err != nil {
		t.Fatalf("execute _panel_stories.html: %v", err)
	}

	rendered := buf.String()
	rowOpen := strings.Index(rendered, `<tr class="story-task-row"`)
	if rowOpen < 0 {
		t.Fatalf("rendered template has no <tr class=\"story-task-row\">; rendered=%s", rendered)
	}
	rowClose := strings.Index(rendered[rowOpen:], "</tr>")
	if rowClose < 0 {
		t.Fatalf("rendered story-task-row has no </tr>")
	}
	return scanTDClasses(rendered[rowOpen : rowOpen+rowClose])
}

// extractAppendContractRowJSCells reads pages/static/common.js,
// locates _appendContractRow, and scans tr.innerHTML for the
// ordered td class list.
func extractAppendContractRowJSCells(t *testing.T) []string {
	t.Helper()
	source := readCommonJS(t)
	body := extractJSFunctionBody(t, source, "_appendContractRow(")
	rowInner := isolateInnerHTMLAssignment(t, body, "tr.innerHTML")
	return scanTDClasses(rowInner)
}
