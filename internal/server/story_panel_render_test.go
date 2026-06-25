package server

import (
	"strings"
	"testing"
	"time"
)

// TestStoryPanelRendersRowContract pins the server-rendered markup the client
// story panel (story_panel.js + Alpine) binds to — the CI-running half of the
// sty_af7d7b1a sweep (it replaces the markup-level assertions the quarantined
// dev-login→navigate chromedp tests carried; the live JS behaviour is proven
// by the build-tagged static-page tests in story_panel_client_test.go). If the
// template drops a data-* attribute or an Alpine binding the client reads, this
// fails in CI without a browser.
func TestStoryPanelRendersRowContract(t *testing.T) {
	html := renderProjectDetail(t, projectDetailData{
		Project:       projectRow{ID: "proj_x", Name: "panel"},
		StoryFiltered: 1,
		StoryTotal:    2,
		EngOrangeSecs: 300,
		EngRedSecs:    900,
		Stories: []storyRow{{
			ID:         "sty_alpha",
			Title:      "alpha story",
			Status:     "in_progress",
			StatusRank: 1,
			Priority:   "high",
			Category:   "feature",
			Tags:       []string{"area:portal", "epic:test"},
			UpdatedAt:  time.Date(2026, 5, 27, 16, 0, 0, 0, time.UTC),
		}},
	})

	// Panel scaffold the client mounts on.
	for _, want := range []string{
		`x-data="storyPanel"`,
		`data-field="panel-stories-search"`,
		`data-field="stories-table"`,
		`data-field="stories-tbody"`,
		`data-field="stories-count-indicator"`,
		`data-field="panel-stories-chips"`,
		`x-for="chip in getEffectiveChips()"`,
		`data-eng-orange-secs="300"`,
		`data-eng-red-secs="900"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("panel scaffold missing %q", want)
		}
	}

	// Story-row data-* attributes + Alpine bindings the filter/order read.
	for _, want := range []string{
		`class="story-row"`,
		`data-id="sty_alpha"`,
		`data-status="in_progress"`,
		`data-status-rank="1"`,
		`data-priority="high"`,
		`data-category="feature"`,
		`data-title="alpha story"`,
		`data-tags="area:portal epic:test "`,
		`x-show="matchesRow($el)"`,
		`@click="toggleRow"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("story-row missing %q", want)
		}
	}
	// data-search must carry id + title + tags (the free-text haystack).
	if !strings.Contains(html, `data-search="sty_alpha alpha story area:portal epic:test "`) {
		t.Errorf("story-row data-search haystack not as expected:\n%s", html)
	}

	// Category + tag click-chips (addCategoryToQuery / addTagToQuery).
	for _, want := range []string{
		`class="category-chip is-clickable" data-category="feature"`,
		`@click.stop="addCategoryToQuery"`,
		`class="tag-chip is-clickable" data-tag="area:portal"`,
		`@click.stop="addTagToQuery"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("row chip markup missing %q", want)
		}
	}

	// Inline detail panel: tab scaffold + lazy-load ledger/documents placeholders
	// (Documents tab sty_bf2fc8e1).
	for _, want := range []string{
		`data-field="story-tabs"`,
		`data-tab="description"`,
		`@click.stop="detailTab='description'"`,
		`data-tab="documents"`,
		`@click.stop="openDocuments('sty_alpha')"`,
		`data-tabpanel="documents"`,
		`data-field="story-documents"`,
		`@click.stop="openLedger('sty_alpha')"`,
		`data-tabpanel="ledger"`,
		`data-field="story-ledger"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("inline detail panel missing %q", want)
		}
	}
	// AC1: the standalone acceptance-criteria block is gone from the inline panel
	// (ACs live in the description body).
	if strings.Contains(html, `data-field="story-acceptance"`) {
		t.Errorf("inline panel still renders a separate acceptance-criteria block:\n%s", html)
	}
}

// TestStoryPanelNoEngagementSpinner pins sty_6cfaa15e: the title-row activity
// spinner is gone — even a row with an engagement signal renders no
// activity-spinner span (the in-progress signal moved to the REVIEWS column,
// where the current-step light pulses via CSS). The current light still renders
// with its review-light-current class, which carries the pulse animation.
func TestStoryPanelNoEngagementSpinner(t *testing.T) {
	html := renderProjectDetail(t, projectDetailData{
		Project:       projectRow{ID: "proj_x", Name: "panel"},
		EngOrangeSecs: 300,
		EngRedSecs:    900,
		Stories: []storyRow{
			{
				ID: "sty_live", Title: "engaged", Status: "in_progress",
				EngLastSeen:   "2026-05-27T16:00:00Z",
				EngLeaseUntil: "2026-05-27T18:00:00Z",
				Reviews:       []reviewLight{{Index: 1, State: "current", Gate: "satellites-intent-plan-review"}},
			},
		},
	})

	if strings.Contains(html, "activity-spinner") {
		t.Errorf("activity-spinner must no longer be rendered:\n%s", html)
	}
	// The current-step light (the new pulsing in-progress signal) is present.
	if !strings.Contains(html, `review-light review-light-current`) {
		t.Errorf("current-step light missing — the in-progress signal:\n%s", html)
	}
}
