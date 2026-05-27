package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html/template"
	"net/http"
	"time"

	"github.com/bobmcallan/satellites/internal/arbor"
)

var apiKeysTmpl = template.Must(template.ParseFS(assets, "templates/api_keys.html", "templates/_user_menu.html"))

type apiKeysData struct {
	Title       string
	UserEmail   string
	UserName    string
	UserAvatar  string
	ActiveNav   string
	Keys        []apiKeyRow
	JustIssued  *issuedKey // non-nil immediately after a successful POST issue
	FlashError  string
	DevMode     bool
	FooterName  string
	FooterEmail string
	Version     string
}

type apiKeyRow struct {
	ID        string
	AgentName string
	CreatedAt time.Time
}

type issuedKey struct {
	ID     string
	Raw    string
	Agent  string
	Notice string // human-friendly reminder that this is the only time the raw value is shown
}

func apiKeysHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := cfg.Sessions.UserID(r)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		switch r.Method {
		case http.MethodGet:
			renderAPIKeys(w, r, cfg, userID, nil, "")
		case http.MethodPost:
			handleAPIKeysPost(w, r, cfg, userID)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func handleAPIKeysPost(w http.ResponseWriter, r *http.Request, cfg Config, userID string) {
	if err := r.ParseForm(); err != nil {
		renderAPIKeys(w, r, cfg, userID, nil, "bad form")
		return
	}
	switch r.FormValue("action") {
	case "issue":
		agentName := r.FormValue("agent_name")
		if agentName == "" {
			agentName = "cli_default"
		}
		keyID := fmt.Sprintf("apk_ui_%s", randomHex8())
		raw, k, err := cfg.Store.IssueAPIKey(r.Context(), keyID, userID, "", agentName)
		if err != nil {
			arbor.ErrorCtx(r.Context(), "apikeys: issue", "user_id", userID, "err", err)
			renderAPIKeys(w, r, cfg, userID, nil, "could not issue key")
			return
		}
		issued := &issuedKey{
			ID:     k.ID,
			Raw:    raw,
			Agent:  k.AgentName,
			Notice: "Copy this key now — it cannot be retrieved later.",
		}
		renderAPIKeys(w, r, cfg, userID, issued, "")
	case "revoke":
		id := r.FormValue("id")
		if id == "" {
			renderAPIKeys(w, r, cfg, userID, nil, "missing id")
			return
		}
		if err := cfg.Store.RevokeKeyForUser(r.Context(), userID, id); err != nil {
			arbor.WarnCtx(r.Context(), "apikeys: revoke", "user_id", userID, "id", id, "err", err)
			renderAPIKeys(w, r, cfg, userID, nil, "could not revoke key")
			return
		}
		http.Redirect(w, r, "/settings/api-keys", http.StatusSeeOther)
	default:
		renderAPIKeys(w, r, cfg, userID, nil, "unknown action")
	}
}

func renderAPIKeys(w http.ResponseWriter, r *http.Request, cfg Config, userID string, issued *issuedKey, flashErr string) {
	keys, err := cfg.Store.ListAPIKeysForUser(r.Context(), userID)
	if err != nil {
		arbor.ErrorCtx(r.Context(), "apikeys: list", "user_id", userID, "err", err)
		http.Error(w, "could not list keys", http.StatusInternalServerError)
		return
	}
	rows := make([]apiKeyRow, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, apiKeyRow{ID: k.ID, AgentName: displayAgent(k.AgentName), CreatedAt: k.CreatedAt})
	}

	var userEmail, userName, userAvatar string
	if u, err := cfg.Store.GetUserByID(r.Context(), userID); err == nil && u != nil {
		userEmail = u.Email
		userName = u.DisplayName
		userAvatar = avatarLetter(u.DisplayName, u.Email)
	}

	data := apiKeysData{
		Title:       "api-keys · satellites",
		UserEmail:   userEmail,
		UserName:    userName,
		UserAvatar:  userAvatar,
		ActiveNav:   "api-keys",
		Keys:        rows,
		JustIssued:  issued,
		FlashError:  flashErr,
		DevMode:     cfg.DevMode,
		FooterName:  footerName,
		FooterEmail: footerEmail,
		Version:     versionString(),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := apiKeysTmpl.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func displayAgent(a string) string {
	if a == "" {
		return "(unset)"
	}
	return a
}

// randomHex8 returns 8 hex chars of crypto-backed entropy. Used as the
// suffix on api-key row ids generated through the UI.
func randomHex8() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
