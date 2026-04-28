package portal

import (
	"regexp"
	"strings"
	"testing"
)

// TestPortalCSS_PortalMainMatchesNavWidth covers AC1 of story_693691ad:
// .portal-main, .nav-inner, and .footer-inner share the same horizontal
// extent so the workspace content edge aligns with the nav strip on wide
// viewports.
func TestPortalCSS_PortalMainMatchesNavWidth(t *testing.T) {
	t.Parallel()
	src := readCSS(t)

	mainWidth := extractMaxWidth(t, src, ".portal-main")
	navWidth := extractMaxWidth(t, src, ".nav-inner")
	footerWidth := extractMaxWidth(t, src, ".footer-inner")

	if mainWidth != navWidth || mainWidth != footerWidth {
		t.Errorf("max-width mismatch — .portal-main=%q, .nav-inner=%q, .footer-inner=%q (must be identical for AC1 of story_693691ad)",
			mainWidth, navWidth, footerWidth)
	}
}

// TestPortalCSS_PanelBodyHasPadding covers AC2 of story_693691ad:
// .panel-body carries an explicit non-zero padding so children never butt
// against the panel border.
func TestPortalCSS_PanelBodyHasPadding(t *testing.T) {
	t.Parallel()
	src := readCSS(t)
	body := ruleBody(t, src, ".panel-body")
	pattern := regexp.MustCompile(`padding\s*:\s*([^;]+);`)
	match := pattern.FindStringSubmatch(body)
	if match == nil {
		t.Fatalf(".panel-body rule must declare padding for AC2 of story_693691ad; body=%q", body)
	}
	value := strings.TrimSpace(match[1])
	if value == "0" || value == "0 0" || value == "0 0 0 0" || strings.HasPrefix(value, "0 0 0") {
		t.Errorf(".panel-body padding must be non-zero (got %q) for AC2 of story_693691ad", value)
	}
}

// extractMaxWidth pulls the max-width value from the named rule. Fails
// the test when the rule or property is missing.
func extractMaxWidth(t *testing.T, src, selector string) string {
	t.Helper()
	body := ruleBody(t, src, selector)
	pattern := regexp.MustCompile(`max-width\s*:\s*([^;]+);`)
	match := pattern.FindStringSubmatch(body)
	if match == nil {
		t.Fatalf("%s rule has no max-width declaration; body=%q", selector, body)
	}
	return strings.TrimSpace(match[1])
}
