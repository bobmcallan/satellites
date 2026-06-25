//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
	"github.com/chromedp/chromedp"
)

// TestWorkspaceDocumentAdd is the DOGFOOD for sty_5d97b972
// (epic:workspace-admin-followup · portal-document-add-ui): the workspace
// documents panel offers a working add-document control (drag-drop + browse)
// wired to a session-authenticated route that reuses the existing ingestion
// path, with fail-closed authz and size/type policy.
func TestWorkspaceDocumentAdd(t *testing.T) {
	env := testbootstrap.SetUpWithServer(t)

	wsStore := workspace.New(env.DB)
	pjStore := project.New(env.DB)
	docStore := document.New(env.DB)
	verb.SetAuthStore(env.Store)
	verb.SetWorkspaceStore(wsStore)
	verb.SetProjectStore(pjStore)
	verb.SetDocumentStore(docStore)
	t.Cleanup(func() {
		verb.SetAuthStore(nil)
		verb.SetWorkspaceStore(nil)
		verb.SetProjectStore(nil)
		verb.SetDocumentStore(nil)
	})

	ctx := context.Background()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	admin, err := env.Store.GetUserByID(ctx, "usr_dev_admin")
	if err != nil {
		t.Fatalf("get admin: %v", err)
	}
	// Owned by the dev admin → admin is a member; the dev user is NOT a member.
	ws, err := wsStore.Create(ctx, admin.ID, "doc-add-demo", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	// --- AC1/AC2/AC6 (happy): add a markdown doc via the browse control and see
	// it land in the panel after reload, driven through the real browser. ---
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), chromedpHeadlessOpts()...)
	t.Cleanup(cancelAlloc)
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	t.Cleanup(cancelBrowser)
	bctx, cancelRun := context.WithTimeout(browserCtx, 60*time.Second)
	t.Cleanup(cancelRun)

	mdPath := filepath.Join(t.TempDir(), "release-notes.md")
	if err := os.WriteFile(mdPath, []byte("# Release Notes\n\nThe corpus objective is steady delivery."), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	var docCountBefore, docCountAfter int
	if err := chromedp.Run(bctx,
		chromedp.Navigate(env.ServerURL+"/login"),
		chromedp.WaitVisible(`form[data-form="login"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-action="dev-login-admin"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-section="server"]`, chromedp.ByQuery),
		chromedp.Navigate(env.ServerURL+"/workspaces/"+ws.ID),
		chromedp.WaitVisible(`[data-field="doc-upload"]`, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelectorAll('[data-field="document-row"]').length`, &docCountBefore),
		// Drive the browse path: set the file input, then fire change as the
		// picker would, so the inline upload script runs and reloads on success.
		chromedp.SetUploadFiles(`[data-field="doc-file-input"]`, []string{mdPath}, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('[data-field="doc-file-input"]').dispatchEvent(new Event('change',{bubbles:true}))`, nil),
		chromedp.WaitVisible(`[data-field="document-row"]`, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelectorAll('[data-field="document-row"]').length`, &docCountAfter),
	); err != nil {
		t.Fatalf("browse upload flow: %v", err)
	}
	if docCountBefore != 0 {
		t.Fatalf("precondition: expected an empty corpus, saw %d doc rows", docCountBefore)
	}
	if docCountAfter < 1 {
		t.Fatalf("uploaded document did not appear in the panel after reload (rows=%d)", docCountAfter)
	}

	// --- AC4/AC6 (fail-closed): session-authenticated HTTP requests cover the
	// authz + size/type policy that the browser path can't easily exercise. ---
	login := func(email, pw string) *http.Client {
		t.Helper()
		jar, _ := cookiejar.New(nil)
		c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := c.PostForm(env.ServerURL+"/login", url.Values{"email": {email}, "password": {pw}})
		if err != nil {
			t.Fatalf("login %s: %v", email, err)
		}
		resp.Body.Close()
		if len(jar.Cookies(mustURL(t, env.ServerURL))) == 0 {
			t.Fatalf("login %s set no session cookie (status %d)", email, resp.StatusCode)
		}
		return c
	}
	postDoc := func(c *http.Client, filename, contentType string, content []byte) *http.Response {
		t.Helper()
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		hdr := make(textproto.MIMEHeader)
		hdr.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename))
		if contentType != "" {
			hdr.Set("Content-Type", contentType)
		}
		fw, err := mw.CreatePart(hdr)
		if err != nil {
			t.Fatalf("create part: %v", err)
		}
		if _, err := fw.Write(content); err != nil {
			t.Fatalf("write part: %v", err)
		}
		mw.Close()
		req, _ := http.NewRequest(http.MethodPost, env.ServerURL+"/workspaces/"+ws.ID+"/documents", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		resp, err := c.Do(req)
		if err != nil {
			t.Fatalf("post doc: %v", err)
		}
		return resp
	}

	adminClient := login(auth.DevAdminEmail, auth.DevAdminPassword)
	userClient := login(auth.DevUserEmail, auth.DevUserPassword)

	// Non-member upload is refused 403 (the dev user is not a member of ws).
	if resp := postDoc(userClient, "sneak.md", "text/markdown", []byte("nope")); resp.StatusCode != http.StatusForbidden {
		resp.Body.Close()
		t.Fatalf("non-member upload status = %d, want 403", resp.StatusCode)
	} else {
		resp.Body.Close()
	}

	// An image now uploads successfully (sty_49a3762e) — accepted as a
	// metadata-only document, status 201.
	if resp := postDoc(adminClient, "logo.png", "image/png", []byte("\x89PNG\r\n\x1a\n")); resp.StatusCode != http.StatusCreated {
		resp.Body.Close()
		t.Fatalf("image upload status = %d, want 201", resp.StatusCode)
	} else {
		resp.Body.Close()
	}

	// A genuinely unsupported binary is still rejected 400 with a clear message
	// (not a 500).
	if resp := postDoc(adminClient, "data.bin", "application/octet-stream", []byte{0x00, 0x01, 0x02}); resp.StatusCode != http.StatusBadRequest {
		resp.Body.Close()
		t.Fatalf("unsupported-type upload status = %d, want 400", resp.StatusCode)
	} else {
		resp.Body.Close()
	}

	// Oversize upload is rejected 400 (not a 500). 33 MiB exceeds the 32 MiB cap.
	if resp := postDoc(adminClient, "huge.md", "text/markdown", bytes.Repeat([]byte("a"), 33<<20)); resp.StatusCode != http.StatusBadRequest {
		resp.Body.Close()
		t.Fatalf("oversize upload status = %d, want 400", resp.StatusCode)
	} else {
		resp.Body.Close()
	}

	// A member (admin) upload over the same route succeeds (201).
	if resp := postDoc(adminClient, "ok.md", "text/markdown", []byte("# ok\n\nbody")); resp.StatusCode != http.StatusCreated {
		resp.Body.Close()
		t.Fatalf("member upload status = %d, want 201", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url %q: %v", raw, err)
	}
	return u
}
