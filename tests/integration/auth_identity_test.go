//go:build integration

package integration_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/invitation"
	"github.com/bobmcallan/satellites/internal/workspace"
	"github.com/bobmcallan/satellites/tests/integration/testbootstrap"
)

// TestUpsertOAuthUserLinksByEmail pins sty_480dba9b: the satellites account is
// keyed by email — an OAuth login for an existing email-only account links the
// credential onto that row (one account), is idempotent, mints a fresh account
// for a new email, and conflicts on a second distinct identity for one email.
func TestUpsertOAuthUserLinksByEmail(t *testing.T) {
	env := testbootstrap.SetUp(t)
	testbootstrap.Reset(t, env)

	ctx := context.Background()
	authStore := auth.New(env.DB)

	// An email-only (basic-auth-style) account.
	base, err := authStore.CreateUser(ctx, "usr_link_base", "link@id.local", "Link Base", auth.RoleUser)
	if err != nil {
		t.Fatalf("create base: %v", err)
	}

	// OAuth login with the SAME email links to the existing row.
	linked, err := authStore.UpsertOAuthUser(ctx, "google", "sub-123", "link@id.local", "Link Google", nil)
	if err != nil {
		t.Fatalf("oauth link: %v", err)
	}
	if linked.ID != base.ID {
		t.Fatalf("oauth login created a new row %q instead of linking to %q", linked.ID, base.ID)
	}

	// Re-login is idempotent (now resolved via the (provider, sub) update).
	again, err := authStore.UpsertOAuthUser(ctx, "google", "sub-123", "link@id.local", "Link Google", nil)
	if err != nil || again.ID != base.ID {
		t.Fatalf("re-login id=%q err=%v, want %q", again.ID, err, base.ID)
	}

	// A brand-new email mints a fresh account.
	fresh, err := authStore.UpsertOAuthUser(ctx, "google", "sub-999", "fresh@id.local", "Fresh", nil)
	if err != nil {
		t.Fatalf("fresh oauth: %v", err)
	}
	if fresh.ID == base.ID {
		t.Fatal("fresh email should not resolve to the base account")
	}

	// A different identity claiming an email already bound to one → conflict.
	if _, err := authStore.UpsertOAuthUser(ctx, "github", "gh-1", "fresh@id.local", "Imposter", nil); err == nil {
		t.Fatal("a second distinct identity for one email should conflict")
	} else if !strings.Contains(err.Error(), "already linked") {
		t.Fatalf("unexpected conflict error: %v", err)
	}
}

// TestPasswordLoginClaimsInvite pins AC#3 from the basic-auth side: a pending
// invitation for the user's email is claimed when they sign in via the
// email+password /login path (not only OAuth).
func TestPasswordLoginClaimsInvite(t *testing.T) {
	env := testbootstrap.SetUpWithServer(t)
	ctx := context.Background()
	now := time.Now().UTC()

	wsStore := workspace.New(env.DB)
	invStore := invitation.New(env.DB)

	adminWS, err := wsStore.GetPersonalForUser(ctx, "usr_dev_admin")
	if err != nil {
		t.Fatalf("admin workspace: %v", err)
	}
	// Invite the dev USER's email into the admin's workspace; pending until login.
	if _, err := invStore.Create(ctx, invitation.CreateInput{
		Email: auth.DevUserEmail, Scope: invitation.ScopeWorkspace,
		WorkspaceID: adminWS.ID, Role: workspace.RoleMember, InvitedBy: "usr_dev_admin",
	}, now); err != nil {
		t.Fatalf("create invite: %v", err)
	}

	// Not a member yet.
	if _, err := wsStore.GetRole(ctx, adminWS.ID, "usr_dev_user"); err == nil {
		t.Fatal("dev user should not be a member before login")
	}

	// Password login via the /login form endpoint.
	jar := &cookieJar{}
	client := &http.Client{Jar: jar}
	resp, err := client.PostForm(env.ServerURL+"/login", url.Values{
		"email":    {auth.DevUserEmail},
		"password": {auth.DevUserPassword},
	})
	if err != nil {
		t.Fatalf("login post: %v", err)
	}
	resp.Body.Close()

	// The pending invite was claimed on the password login → membership exists.
	role, err := wsStore.GetRole(ctx, adminWS.ID, "usr_dev_user")
	if err != nil {
		t.Fatalf("membership after password login: %v", err)
	}
	if role != workspace.RoleMember {
		t.Fatalf("claimed role = %q, want member", role)
	}
}

// cookieJar is a minimal in-memory jar so the login redirect chain carries the
// session cookie (net/http requires a jar to retain Set-Cookie across hops).
type cookieJar struct{ cookies []*http.Cookie }

func (j *cookieJar) SetCookies(_ *url.URL, cs []*http.Cookie) { j.cookies = append(j.cookies, cs...) }
func (j *cookieJar) Cookies(_ *url.URL) []*http.Cookie        { return j.cookies }
