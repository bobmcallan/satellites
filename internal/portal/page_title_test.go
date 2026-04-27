package portal

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBuildPageTitle (story_f7152e83) — verifies the SSR <title> pattern
// "SATELLITES — <Project|Workspace>[ — <page>]" with em-dash separators.
func TestBuildPageTitle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		active      wsChip
		projectName string
		pageName    string
		expected    string
	}{
		{
			name:     "no workspace, no project, no page",
			expected: "SATELLITES",
		},
		{
			name:     "workspace only",
			active:   wsChip{Name: "Default"},
			expected: "SATELLITES — Default",
		},
		{
			name:     "workspace and page",
			active:   wsChip{Name: "Default"},
			pageName: "projects",
			expected: "SATELLITES — Default — projects",
		},
		{
			name:        "project takes precedence over workspace",
			active:      wsChip{Name: "Default"},
			projectName: "MyProject",
			expected:    "SATELLITES — MyProject",
		},
		{
			name:        "project and page",
			active:      wsChip{Name: "Default"},
			projectName: "MyProject",
			pageName:    "stories",
			expected:    "SATELLITES — MyProject — stories",
		},
		{
			name:     "page only (no workspace)",
			pageName: "settings",
			expected: "SATELLITES — settings",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := buildPageTitle(tc.active, tc.projectName, tc.pageName)
			assert.Equal(t, tc.expected, got)
		})
	}
}

// TestBuildPageTitle_EmDashSeparator (story_f7152e83 AC3) — every separator
// between segments is U+2014 em-dash, never hyphen or en-dash.
func TestBuildPageTitle_EmDashSeparator(t *testing.T) {
	t.Parallel()

	got := buildPageTitle(wsChip{Name: "ws"}, "proj", "page")
	assert.Contains(t, got, " — ", "em-dash separator U+2014 expected")
	assert.NotContains(t, got, " - ", "ascii hyphen separator forbidden")
	assert.NotContains(t, got, " – ", "en-dash separator forbidden")

	// Byte-level: em-dash is the 3-byte UTF-8 sequence E2 80 94.
	idx := strings.Index(got, "—")
	assert.NotEqual(t, -1, idx, "U+2014 must appear in the title")
}

// TestBuildPageTitle_BrandFirst (story_f7152e83 AC1, AC2) — the title
// always starts with uppercase SATELLITES.
func TestBuildPageTitle_BrandFirst(t *testing.T) {
	t.Parallel()

	cases := []struct {
		active      wsChip
		projectName string
		pageName    string
	}{
		{},
		{active: wsChip{Name: "ws"}},
		{projectName: "proj"},
		{active: wsChip{Name: "ws"}, projectName: "proj", pageName: "page"},
	}
	for _, c := range cases {
		got := buildPageTitle(c.active, c.projectName, c.pageName)
		assert.True(t, strings.HasPrefix(got, "SATELLITES"), "title %q must start with SATELLITES", got)
	}
}
