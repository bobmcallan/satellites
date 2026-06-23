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
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestProjectDocumentAdd is the regression for sty_a9c5a33d: a project document
// upload (POST /projects/{id}/documents) must succeed (201), not 500. The handler
// previously built its BlobUpload without a workspace_id, so blob.Store.Create
// rejected it ("workspace_id required") and the route returned 500. The fix resolves
// the project's workspace_id from project_get and passes it. This test drives the
// real session route end-to-end, so it fails against the pre-fix handler.
func TestProjectDocumentAdd(t *testing.T) {
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
	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	admin, err := env.Store.GetUserByID(ctx, "usr_dev_admin")
	if err != nil {
		t.Fatalf("get admin: %v", err)
	}
	// Owned by the dev admin → admin is a member; the dev user is NOT a member.
	ws, err := wsStore.Create(ctx, admin.ID, "proj-doc-demo", now)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	pj, err := pjStore.Create(ctx, project.CreateInput{
		WorkspaceID: ws.ID,
		Name:        "proj-doc-demo-project",
		OwnerUserID: admin.ID,
	}, now)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

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
		req, _ := http.NewRequest(http.MethodPost, env.ServerURL+"/projects/"+pj.ID+"/documents", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		resp, err := c.Do(req)
		if err != nil {
			t.Fatalf("post doc: %v", err)
		}
		return resp
	}

	adminClient := login(auth.DevAdminEmail, auth.DevAdminPassword)
	userClient := login(auth.DevUserEmail, auth.DevUserPassword)

	// A member (admin) upload to the project route succeeds (201) — the regression:
	// the pre-fix handler returned 500 here because workspace_id was never resolved.
	if resp := postDoc(adminClient, "spec.md", "text/markdown", []byte("# Spec\n\nbody")); resp.StatusCode != http.StatusCreated {
		resp.Body.Close()
		t.Fatalf("member project upload status = %d, want 201 (was 500 pre-fix)", resp.StatusCode)
	} else {
		resp.Body.Close()
	}

	// Non-member upload is refused 403 (the dev user is not a member of the project).
	if resp := postDoc(userClient, "sneak.md", "text/markdown", []byte("nope")); resp.StatusCode != http.StatusForbidden {
		resp.Body.Close()
		t.Fatalf("non-member project upload status = %d, want 403", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
}
