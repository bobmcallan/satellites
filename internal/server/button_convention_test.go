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

// chromeButtonFamilies are class-name substrings marking a decorative/navigation
// button that is EXEMPT from the .btn convention (styles.css: "Decorative buttons
// … are chrome, not form controls, and keep their bespoke styling"): filter/tag
// chips, the avatar/menu trigger, modal-close, expand toggles, and search-clear
// affordances. Substring-matched so a sibling variant (panel-filter-chip-clear,
// etc.) needs no test edit. role="tab"/"menuitem" buttons are chrome too.
var chromeButtonFamilies = []string{"chip", "avatar", "modal-close", "expand-toggle", "search-clear"}

// isChromeButton reports whether a <button> open tag is decorative chrome (and so
// exempt from the .btn action-button convention).
func isChromeButton(tag string, classes []string) bool {
	if strings.Contains(tag, `role="tab"`) || strings.Contains(tag, `role="menuitem"`) {
		return true
	}
	for _, c := range classes {
		for _, fam := range chromeButtonFamilies {
			if strings.Contains(c, fam) {
				return true
			}
		}
	}
	return false
}

// TestActionButtonsUseGlobalStyleAcrossTemplates extends the global-button
// convention (sty_f6fdce76) from the invite surface to EVERY template: every
// action <button> repo-wide carries the canonical `.btn` class and no inline
// style; decorative chrome (chromeButtonClasses / role="tab") is exempt. This is
// what keeps the one slimmed `.btn` rule the single source of button styling.
func TestActionButtonsUseGlobalStyleAcrossTemplates(t *testing.T) {
	entries, err := assets.ReadDir("templates")
	if err != nil {
		t.Fatalf("read templates dir: %v", err)
	}
	buttonOpen := regexp.MustCompile(`(?s)<button\b[^>]*>`)
	classAttr := regexp.MustCompile(`class="([^"]*)"`)
	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".html") {
			continue
		}
		body, err := assets.ReadFile("templates/" + e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, tag := range buttonOpen.FindAllString(string(body), -1) {
			classes := strings.Fields(func() string {
				if m := classAttr.FindStringSubmatch(tag); m != nil {
					return m[1]
				}
				return ""
			}())
			if isChromeButton(tag, classes) {
				continue
			}
			checked++
			if strings.Contains(tag, "style=") {
				t.Errorf("%s: action button has inline style (use a .btn variant): %s", e.Name(), tag)
			}
			if !containsString(classes, "btn") {
				t.Errorf("%s: action button not using the global .btn class: %s", e.Name(), tag)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no action buttons scanned — exemption too broad or templates moved?")
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
