package server

import (
	"encoding/json"
	"html/template"
	"net/http"
	"strings"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/verb"
)

var inviteRedeemTmpl = template.Must(template.ParseFS(assets, "templates/invite_redeem.html"))

type inviteRedeemData struct {
	Title       string
	Version     string
	FooterName  string
	FooterEmail string
	Token       string
	SignedIn    bool
	Error       string
}

// inviteRedeemHandler serves GET/POST /invite/{token}: the page a manually-sent
// invite link lands on. GET shows a confirm (signed-in) or sign-in prompt;
// POST dispatches invitation_redeem as the session user and lands them on the
// project. Authorization is possession of the token — the verb gate is just
// "must be signed in".
func inviteRedeemHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSpace(r.PathValue("token"))
		userID, err := cfg.Sessions.UserID(r)
		signedIn := err == nil && userID != ""

		switch r.Method {
		case http.MethodGet:
			renderInviteRedeem(w, cfg, token, signedIn, "")
		case http.MethodPost:
			if !signedIn {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			ctx := withSessionUser(r.Context(), cfg, userID)
			body, _ := json.Marshal(verb.InvitationRedeemRequest{Token: token})
			raw, derr := verb.Dispatch(ctx, "invitation_redeem", body)
			if derr != nil {
				arbor.WarnCtx(ctx, "invite redeem", "user_id", userID, "err", derr)
				renderInviteRedeem(w, cfg, token, true, friendlyRedeemErr(derr))
				return
			}
			var inv struct {
				Scope     string `json:"scope"`
				ProjectID string `json:"project_id"`
			}
			_ = json.Unmarshal(raw, &inv)
			dest := "/"
			if inv.Scope == "project" && inv.ProjectID != "" {
				dest = "/projects/" + inv.ProjectID
			}
			http.Redirect(w, r, dest, http.StatusSeeOther)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func renderInviteRedeem(w http.ResponseWriter, cfg Config, token string, signedIn bool, errMsg string) {
	data := inviteRedeemData{
		Title:       "accept invite · satellites",
		Version:     versionString(),
		FooterName:  footerName,
		FooterEmail: footerEmail,
		Token:       token,
		SignedIn:    signedIn,
		Error:       errMsg,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := inviteRedeemTmpl.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// friendlyRedeemErr maps an invitation_redeem error to recipient-facing text.
func friendlyRedeemErr(err error) string {
	switch s := err.Error(); {
	case strings.Contains(s, "not_found"):
		return "This invite link is not valid."
	case strings.Contains(s, "already_redeemed"):
		return "This invite link has already been used."
	case strings.Contains(s, "expired"):
		return "This invite link has expired."
	default:
		return "Could not accept this invite. Please ask for a new link."
	}
}
