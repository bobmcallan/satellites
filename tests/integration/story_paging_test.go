//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/ledger"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/server"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestStoryPanelServerSidePaging exercises sty_47234d6e: the default view filters
// status:open server-side, so each page is a full page of matching rows, and the
// indicator reads filtered/total (matching count / all-status count) with page
// counts over the filtered set. 60 open + 5 done = 65 total; page size 50.
func TestStoryPanelServerSidePaging(t *testing.T) {
	env := testbootstrap.SetUp(t)
	bg := context.Background()
	now := time.Date(2026, 5, 25, 16, 0, 0, 0, time.UTC)

	store := auth.New(env.DB)
	if err := store.DevSeed(bg); err != nil {
		t.Fatalf("dev seed: %v", err)
	}
	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	verb.SetAuthStore(store)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	verb.SetDocumentStore(document.New(env.DB))
	verb.SetLedgerStore(ledger.New(env.DB))
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetDocumentStore(nil)
		verb.SetLedgerStore(nil)
	})

	ws, err := wsStore.Create(bg, "", "paging-ws", now)
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	pj, err := pjStore.Create(bg, project.CreateInput{WorkspaceID: ws.ID, Name: "paging-pj"}, now)
	if err != nil {
		t.Fatalf("project: %v", err)
	}

	const open, done = 60, 5
	seed := func(name, status string) {
		st := status
		req, _ := json.Marshal(verb.DocumentUpsertRequest{
			Type: "story", ProjectID: pj.ID, Name: name, Status: &st,
		})
		if _, err := verb.Dispatch(bg, "document_upsert", req); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	for i := 0; i < open; i++ {
		seed("open story "+strconv.Itoa(i), "backlog")
	}
	for i := 0; i < done; i++ {
		seed("done story "+strconv.Itoa(i), "done")
	}

	sessions := auth.NewSessions([]byte("paging-test-secret-0123456789abcd"))
	srv := httptest.NewServer(server.Build(server.Config{Store: store, Sessions: sessions, DevMode: true}))
	t.Cleanup(srv.Close)

	type frag struct {
		filtered, total, pageCount, rows int
	}
	get := func(t *testing.T, query string) frag {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/projects/"+pj.ID+"/stories.fragment"+query, nil)
		rec := httptest.NewRecorder()
		sessions.Issue(rec, "usr_dev_admin")
		for _, c := range rec.Result().Cookies() {
			req.AddCookie(c)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("get %q: %v", query, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d for %q", resp.StatusCode, query)
		}
		buf := make([]byte, 0)
		tmp := make([]byte, 4096)
		for {
			n, e := resp.Body.Read(tmp)
			buf = append(buf, tmp[:n]...)
			if e != nil {
				break
			}
		}
		atoi := func(h string) int { n, _ := strconv.Atoi(resp.Header.Get(h)); return n }
		return frag{
			filtered:  atoi("X-Story-Filtered"),
			total:     atoi("X-Story-Total"),
			pageCount: atoi("X-Story-Page-Count"),
			rows:      strings.Count(string(buf), `data-field="story-row"`),
		}
	}

	// Default view (status:open) — page 1 is a FULL page of 50 open rows;
	// filtered = 60 open, total = 65 all-status, 2 pages over the filtered set.
	p1 := get(t, "")
	if p1.filtered != open || p1.total != open+done {
		t.Fatalf("default p1: filtered=%d total=%d, want %d/%d", p1.filtered, p1.total, open, open+done)
	}
	if p1.pageCount != 2 {
		t.Fatalf("default page count = %d, want 2 (over filtered set)", p1.pageCount)
	}
	if p1.rows != 50 {
		t.Fatalf("default p1 rows = %d, want a full page of 50 matching rows (no client-hidden gaps)", p1.rows)
	}

	// Default page 2 — the remaining 10 open rows.
	p2 := get(t, "?stories_page=2")
	if p2.rows != open-50 {
		t.Fatalf("default p2 rows = %d, want %d", p2.rows, open-50)
	}
	if p2.filtered != open {
		t.Fatalf("default p2 filtered = %d, want %d", p2.filtered, open)
	}

	// status:all widens to every story; filtered == total.
	all := get(t, "?stories_q=status:all")
	if all.filtered != open+done || all.total != open+done {
		t.Fatalf("status:all: filtered=%d total=%d, want %d/%d", all.filtered, all.total, open+done, open+done)
	}
	if all.pageCount != 2 { // 65 / 50 = 2
		t.Fatalf("status:all page count = %d, want 2", all.pageCount)
	}
	if all.rows != 50 {
		t.Fatalf("status:all p1 rows = %d, want 50", all.rows)
	}
}
