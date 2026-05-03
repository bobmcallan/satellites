package portal

import (
	"os"
	"strings"
	"testing"
)

// TestMobileView_ViewportMetaPresent asserts head.html declares the
// device-width viewport meta tag. Without it the portal renders at
// desktop layout zoomed out on phones. epic:mobile-view sty_11f60d6d.
func TestMobileView_ViewportMetaPresent(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("../../pages/templates/head.html")
	if err != nil {
		t.Fatalf("read head.html: %v", err)
	}
	want := `<meta name="viewport" content="width=device-width, initial-scale=1">`
	if !strings.Contains(string(src), want) {
		t.Errorf("head.html missing viewport meta tag (epic:mobile-view sty_11f60d6d); want substring %q", want)
	}
}

// TestMobileView_BreakpointBlockPresent asserts the mobile @media
// block exists in portal.css and references the canonical
// `epic:mobile-view` marker so the next reader can find it.
// sty_11f60d6d (anchor) + slices 2/3/4 share the block.
func TestMobileView_BreakpointBlockPresent(t *testing.T) {
	t.Parallel()
	src := readCSS(t)
	if !strings.Contains(src, "epic:mobile-view") {
		t.Fatalf("portal.css missing epic:mobile-view marker comment")
	}
	if !strings.Contains(src, "@media (max-width: 48rem)") {
		t.Fatalf("portal.css missing @media (max-width: 48rem) breakpoint")
	}
}

// TestMobileView_ActionAffordancesHiddenAtMobile asserts the global
// action-hide rule (slice 1, sty_11f60d6d) covers every write-path
// surface so view-mode is enforced by construction at mobile width.
// New action surfaces should be added to this list as they ship.
func TestMobileView_ActionAffordancesHiddenAtMobile(t *testing.T) {
	t.Parallel()
	src := readCSS(t)
	mobileBlock := extractMobileBlock(t, src)
	for _, sel := range []string{
		".story-bulk-bar",
		".story-status-controls",
		".col-select",
		".col-actions",
		".ci-action-btn",
		".status-reason-input",
	} {
		if !strings.Contains(mobileBlock, sel) {
			t.Errorf("mobile @media block missing action-hide selector %q", sel)
		}
	}
}

// TestMobileView_StoriesPanelTableCollapse asserts slice 2
// (sty_f8791f88): the stories panel table layout collapses to block
// rendering at mobile width so each story renders as a card.
func TestMobileView_StoriesPanelTableCollapse(t *testing.T) {
	t.Parallel()
	src := readCSS(t)
	mobileBlock := extractMobileBlock(t, src)
	for _, want := range []string{
		".panel-table-stories thead",
		".panel-table-contracts thead",
		".panel-table-stories tbody tr.story-row",
		".panel-table-contracts tbody tr.story-contract-row",
	} {
		if !strings.Contains(mobileBlock, want) {
			t.Errorf("mobile @media block missing stories-panel collapse rule %q", want)
		}
	}
}

// TestMobileView_TaskFeedAndWalkDensity asserts slice 3
// (sty_dc8349d8): task-table collapses to cards and walk-card density
// tightens at mobile width.
func TestMobileView_TaskFeedAndWalkDensity(t *testing.T) {
	t.Parallel()
	src := readCSS(t)
	mobileBlock := extractMobileBlock(t, src)
	for _, want := range []string{
		".task-table thead",
		".task-table tbody tr",
		".walk-card",
		".walk-card-meta",
	} {
		if !strings.Contains(mobileBlock, want) {
			t.Errorf("mobile @media block missing task-feed/walk rule %q", want)
		}
	}
}

// TestMobileView_NavMetaAndFooterStack asserts slice 4 (sty_ba2c9de8):
// .kv-list collapses to single-column, footer 3-slot row stacks
// vertically, and nav workspace switcher condenses.
func TestMobileView_NavMetaAndFooterStack(t *testing.T) {
	t.Parallel()
	src := readCSS(t)
	mobileBlock := extractMobileBlock(t, src)
	for _, want := range []string{
		".kv-list",
		".footer-inner",
		".footer-slot",
		".nav-workspace",
	} {
		if !strings.Contains(mobileBlock, want) {
			t.Errorf("mobile @media block missing nav/meta/footer rule %q", want)
		}
	}
	// kv-list must collapse to single-column (1fr).
	if !strings.Contains(mobileBlock, "grid-template-columns: 1fr") {
		t.Errorf("mobile @media block must collapse .kv-list to grid-template-columns: 1fr")
	}
	// footer-inner must stack (flex-direction: column).
	if !strings.Contains(mobileBlock, "flex-direction: column") {
		t.Errorf("mobile @media block must stack .footer-inner via flex-direction: column")
	}
}

// extractMobileBlock returns the contents of the epic:mobile-view
// @media block. The block is delimited by its opening `@media (max-width: 48rem) {`
// preceded by the epic:mobile-view marker comment.
func extractMobileBlock(t *testing.T, src string) string {
	t.Helper()
	marker := "epic:mobile-view"
	mIdx := strings.Index(src, marker)
	if mIdx < 0 {
		t.Fatalf("portal.css missing epic:mobile-view marker")
	}
	header := "@media (max-width: 48rem) {"
	hIdx := strings.Index(src[mIdx:], header)
	if hIdx < 0 {
		t.Fatalf("epic:mobile-view marker present but no @media block follows")
	}
	open := mIdx + hIdx + len(header)
	depth := 1
	for i := open; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[open:i]
			}
		}
	}
	t.Fatalf("@media block has no closing brace")
	return ""
}
