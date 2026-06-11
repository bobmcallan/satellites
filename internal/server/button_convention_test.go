package server

import (
	"regexp"
	"strings"
	"testing"
)

// TestInviteFormButtonsUseGlobalStyle locks the global-button convention
// (sty_f5ff9d8a) on the invite/registration surface: every <button> in
// admin_people.html must carry the canonical `.btn` class — no bare or
// inline-styled buttons. This is the convention global-button-style-review-skill
// (story 6) enforces repo-wide.
func TestInviteFormButtonsUseGlobalStyle(t *testing.T) {
	body, err := assets.ReadFile("templates/admin_people.html")
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	html := string(body)

	buttonOpen := regexp.MustCompile(`(?s)<button\b[^>]*>`)
	classAttr := regexp.MustCompile(`class="([^"]*)"`)

	tags := buttonOpen.FindAllString(html, -1)
	if len(tags) == 0 {
		t.Fatal("no <button> tags found — template moved?")
	}
	for _, tag := range tags {
		if strings.Contains(tag, "style=") {
			t.Errorf("button has inline style (use a .btn variant): %s", tag)
		}
		m := classAttr.FindStringSubmatch(tag)
		if m == nil {
			t.Errorf("button has no class (must be a .btn variant): %s", tag)
			continue
		}
		classes := strings.Fields(m[1])
		if !containsString(classes, "btn") {
			t.Errorf("button not using the global .btn class: %s", tag)
		}
	}
}

func containsString(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
