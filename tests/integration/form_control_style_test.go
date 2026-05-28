//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/variable"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
	"github.com/chromedp/chromedp"
)

type fcHeight struct {
	Sel string `json:"sel"`
	H   string `json:"h"`
}

// TestFormControlStyle_UniformHeight pins sty_332acf5d: every form
// control on a page renders at the system `--control-height`. Walks
// inputs, selects, textareas, and `.btn` buttons inside form-shaped
// containers (forms, story-panel, bulk-action bar) and asserts
// pixel-identical computed heights.
//
// The screenshot regression was the bulk-bar `done` dropdown sitting
// shorter than the adjacent `apply to selection` button. This test
// captures that case directly via the bulk-bar selector list.
func TestFormControlStyle_UniformHeight(t *testing.T) {
	env := testbootstrap.SetUpWithServer(t)

	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	docStore := document.New(env.DB)
	varStore := variable.New(env.DB)
	verb.SetAuthStore(env.Store)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	verb.SetDocumentStore(docStore)
	verb.SetVariableStore(varStore)
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetDocumentStore(nil)
		verb.SetVariableStore(nil)
	})

	bgCtx := context.Background()
	now := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	ws, err := wsStore.Create(bgCtx, "", "form-style-ws", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pj, err := pjStore.Create(bgCtx, project.CreateInput{
		WorkspaceID: ws.ID,
		Name:        "form-style-project",
	}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	st, pr := "backlog", "medium"
	tg := []string{"area:test"}
	req, _ := json.Marshal(verb.DocumentUpsertRequest{
		Type:      "story",
		ProjectID: pj.ID,
		Name:      "form-style story one",
		Status:    &st,
		Priority:  &pr,
		Tags:      &tg,
	})
	if _, err := verb.Dispatch(bgCtx, "document_upsert", req); err != nil {
		t.Fatalf("create story: %v", err)
	}

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), chromedpHeadlessOpts()...)
	t.Cleanup(cancelAlloc)
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	t.Cleanup(cancelBrowser)
	bctx, cancelRun := context.WithTimeout(browserCtx, 60*time.Second)
	t.Cleanup(cancelRun)

	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/login"),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-action="dev-login-admin"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("login: %v", err)
	}

	assertUniformHeight := func(t *testing.T, label string, selectors []string) {
		t.Helper()
		selJSON, err := json.Marshal(selectors)
		if err != nil {
			t.Fatalf("marshal selectors: %v", err)
		}
		js := fmt.Sprintf(`(() => {
			const sels = %s;
			const out = [];
			for (const sel of sels) {
				const els = document.querySelectorAll(sel);
				for (const el of els) {
					if (el.offsetParent === null && getComputedStyle(el).display === 'none') continue;
					out.push({sel: sel, h: getComputedStyle(el).height});
				}
			}
			return out;
		})()`, selJSON)
		var got []fcHeight
		if err := chromedp.Run(bctx, chromedp.Evaluate(js, &got)); err != nil {
			t.Fatalf("[%s] evaluate: %v", label, err)
		}
		if len(got) == 0 {
			t.Fatalf("[%s] no form controls matched any selector — test wiring broken", label)
		}
		ref := got[0].H
		for _, g := range got {
			if g.H != ref {
				t.Errorf("[%s] height drift: %s = %s, but %s = %s", label, got[0].Sel, ref, g.Sel, g.H)
			}
		}
		t.Logf("[%s] %d form controls all at height %s", label, len(got), ref)
	}

	t.Run("project_detail with bulk-bar visible", func(t *testing.T) {
		if err := chromedp.Run(bctx,
			chromedp.Navigate(env.ServerURL+"/projects/"+pj.ID),
			chromedp.WaitVisible(`[data-field="stories-table"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`.panel-search`, chromedp.ByQuery),
			chromedp.Click(`input[data-field="story-row-select"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-field="story-bulk-bar"]`, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("nav + select: %v", err)
		}
		assertUniformHeight(t, "project_detail", []string{
			`.panel-search`,
			`.story-bulk-bar select`,
			`.story-bulk-bar button.btn`,
		})
	})

	t.Run("system-kv edit row + add-kv row", func(t *testing.T) {
		if _, err := varStore.SeedSystem(bgCtx, "form-style.test.kv", "1", now); err != nil {
			t.Fatalf("seed kv: %v", err)
		}
		if err := chromedp.Run(bctx,
			chromedp.Navigate(env.ServerURL+"/settings/system-kv"),
			chromedp.WaitVisible(`[data-field="kv-value-input"]`, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("nav system-kv: %v", err)
		}
		assertUniformHeight(t, "system-kv", []string{
			`.kv-input`,
			`.kv-edit-form button.btn`,
			`.kv-delete-form button.btn`,
			`form[data-form="kv-add"] input[type="text"]`,
			`form[data-form="kv-add"] button.btn`,
		})
	})
}
