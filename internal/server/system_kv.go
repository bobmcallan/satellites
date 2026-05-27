package server

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"sort"
	"time"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/verb"
)

var systemKVTmpl = template.Must(
	template.New("system_kv.html").Funcs(template.FuncMap{
		"formatTime": formatRowTime,
	}).ParseFS(assets, "templates/system_kv.html", "templates/_user_menu.html"),
)

type systemKVData struct {
	Title       string
	UserEmail   string
	UserName    string
	UserAvatar  string
	ActiveNav   string
	Stored      []systemKVRow
	Computed    []systemKVRow
	FlashError  string
	FlashOK     string
	DevMode     bool
	FooterName  string
	FooterEmail string
	Version     string
}

type systemKVRow struct {
	Name      string
	Value     string
	UpdatedAt time.Time
}

func systemKVHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := cfg.Sessions.UserID(r)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		ctx := withSessionUser(r.Context(), cfg, userID)

		switch r.Method {
		case http.MethodGet:
			renderSystemKV(w, r.WithContext(ctx), cfg, userID, "", "")
		case http.MethodPost:
			handleSystemKVPost(w, r.WithContext(ctx), cfg, userID)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func handleSystemKVPost(w http.ResponseWriter, r *http.Request, cfg Config, userID string) {
	if err := r.ParseForm(); err != nil {
		renderSystemKV(w, r, cfg, userID, "bad form", "")
		return
	}
	action := r.FormValue("action")
	name := r.FormValue("name")
	switch action {
	case "set":
		if name == "" {
			renderSystemKV(w, r, cfg, userID, "name required", "")
			return
		}
		req := verb.VariableSetRequest{
			Name:  name,
			Scope: "system",
			Value: r.FormValue("value"),
		}
		body, _ := json.Marshal(req)
		if _, err := verb.Dispatch(r.Context(), "variable_set", body); err != nil {
			arbor.WarnCtx(r.Context(), "system_kv: set", "name", name, "err", err)
			renderSystemKV(w, r, cfg, userID, err.Error(), "")
			return
		}
		http.Redirect(w, r, "/settings/system-kv", http.StatusSeeOther)
	case "delete":
		if name == "" {
			renderSystemKV(w, r, cfg, userID, "name required", "")
			return
		}
		req := verb.VariableDeleteRequest{Name: name, Scope: "system"}
		body, _ := json.Marshal(req)
		if _, err := verb.Dispatch(r.Context(), "variable_delete", body); err != nil {
			arbor.WarnCtx(r.Context(), "system_kv: delete", "name", name, "err", err)
			renderSystemKV(w, r, cfg, userID, err.Error(), "")
			return
		}
		http.Redirect(w, r, "/settings/system-kv", http.StatusSeeOther)
	default:
		renderSystemKV(w, r, cfg, userID, "unknown action", "")
	}
}

func renderSystemKV(w http.ResponseWriter, r *http.Request, cfg Config, userID string, flashErr, flashOK string) {
	ctx := r.Context()

	stored, computed, err := loadSystemKV(ctx)
	if err != nil {
		arbor.ErrorCtx(ctx, "system_kv: list", "user_id", userID, "err", err)
		http.Error(w, "could not list system kv", http.StatusInternalServerError)
		return
	}

	var userEmail, userName, userAvatar string
	if cfg.Store != nil && cfg.Store.DB != nil {
		if u, err := cfg.Store.GetUserByID(ctx, userID); err == nil && u != nil {
			userEmail = u.Email
			userName = u.DisplayName
			userAvatar = avatarLetter(u.DisplayName, u.Email)
		}
	}

	data := systemKVData{
		Title:       "system kv · satellites",
		UserEmail:   userEmail,
		UserName:    userName,
		UserAvatar:  userAvatar,
		ActiveNav:   "system-kv",
		Stored:      stored,
		Computed:    computed,
		FlashError:  flashErr,
		FlashOK:     flashOK,
		DevMode:     cfg.DevMode,
		FooterName:  footerName,
		FooterEmail: footerEmail,
		Version:     versionString(),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := systemKVTmpl.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// loadSystemKV dispatches variable_list at scope=system and splits the
// entries into stored vs computed by name. variable_list folds in both
// kinds; the split here is by the well-known computed-name registry
// (mirrored from cmd/satellites-server/main.go's systemVars).
func loadSystemKV(ctx context.Context) ([]systemKVRow, []systemKVRow, error) {
	body, _ := json.Marshal(verb.VariableListRequest{Scope: "system"})
	raw, err := verb.Dispatch(ctx, "variable_list", body)
	if err != nil {
		return nil, nil, err
	}
	var resp verb.VariableListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, nil, err
	}
	stored := make([]systemKVRow, 0)
	computed := make([]systemKVRow, 0)
	for _, e := range resp.Variables {
		row := systemKVRow{Name: e.Name, Value: e.Value}
		if isComputedSystemName(e.Name) {
			computed = append(computed, row)
		} else {
			stored = append(stored, row)
		}
	}
	sort.Slice(stored, func(i, j int) bool { return stored[i].Name < stored[j].Name })
	sort.Slice(computed, func(i, j int) bool { return computed[i].Name < computed[j].Name })
	return stored, computed, nil
}

// isComputedSystemName lists the names served by the computed resolver
// today. Mirrors cmd/satellites-server/main.go's systemVars registry.
// Adding a new computed variable requires extending this list — the
// alternative is plumbing the resolver's name set through the verb
// layer, which adds coupling for a small win.
func isComputedSystemName(name string) bool {
	switch name {
	case "version", "cli_version", "os", "arch", "server_url", "current_version", "state":
		return true
	}
	return false
}
