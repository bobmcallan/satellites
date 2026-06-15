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
	Rows        []systemKVRow
	FlashError  string
	FlashOK     string
	DevMode     bool
	FooterName  string
	FooterEmail string
	Version     string
}

// systemKVRow is one variable in the unified table. Scope makes a value's
// layer visible at a glance (system/user, plus workspace/project when those
// are surfaced); Computed marks a read-only derived system value; Secret marks
// a redacted credential row whose value is never rendered.
type systemKVRow struct {
	Scope     string
	Name      string
	Value     string
	Secret    bool
	Computed  bool
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
	// Scope defaults to system (the page's primary surface); a user-override
	// row posts scope=user, which the verb keys to the caller's own user_id.
	scope := r.FormValue("scope")
	if scope == "" {
		scope = "system"
	}
	switch action {
	case "set":
		if name == "" {
			renderSystemKV(w, r, cfg, userID, "name required", "")
			return
		}
		req := verb.VariableSetRequest{
			Name:  name,
			Scope: scope,
			Value: r.FormValue("value"),
		}
		body, _ := json.Marshal(req)
		if _, err := verb.Dispatch(r.Context(), "variable_set", body); err != nil {
			arbor.WarnCtx(r.Context(), "system_kv: set", "name", name, "scope", scope, "err", err)
			renderSystemKV(w, r, cfg, userID, err.Error(), "")
			return
		}
		http.Redirect(w, r, "/settings/system-kv", http.StatusSeeOther)
	case "delete":
		if name == "" {
			renderSystemKV(w, r, cfg, userID, "name required", "")
			return
		}
		req := verb.VariableDeleteRequest{Name: name, Scope: scope}
		body, _ := json.Marshal(req)
		if _, err := verb.Dispatch(r.Context(), "variable_delete", body); err != nil {
			arbor.WarnCtx(r.Context(), "system_kv: delete", "name", name, "scope", scope, "err", err)
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

	rows, err := loadSystemKV(ctx)
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
		Rows:        rows,
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

// loadSystemKV builds the unified row set: every variable visible on this page
// across scopes, each tagged with its scope so the layer is visible in one
// table. It folds system rows (stored + computed) and the caller's user-scope
// overrides; workspace/project values live on their own pages and are not
// duplicated here. Secret rows arrive already redacted from variable_list.
func loadSystemKV(ctx context.Context) ([]systemKVRow, error) {
	rows := make([]systemKVRow, 0)

	sysBody, _ := json.Marshal(verb.VariableListRequest{Scope: "system"})
	sysRaw, err := verb.Dispatch(ctx, "variable_list", sysBody)
	if err != nil {
		return nil, err
	}
	var sysResp verb.VariableListResponse
	if err := json.Unmarshal(sysRaw, &sysResp); err != nil {
		return nil, err
	}
	for _, e := range sysResp.Variables {
		rows = append(rows, systemKVRow{
			Scope:    "system",
			Name:     e.Name,
			Value:    e.Value,
			Secret:   e.Secret,
			Computed: isComputedSystemName(e.Name),
		})
	}

	// The caller's user-scope overrides (top of the cascade, sty_6cdf1cd0).
	// A missing caller identity simply yields no user rows.
	userBody, _ := json.Marshal(verb.VariableListRequest{Scope: "user"})
	if userRaw, uErr := verb.Dispatch(ctx, "variable_list", userBody); uErr == nil {
		var userResp verb.VariableListResponse
		if err := json.Unmarshal(userRaw, &userResp); err == nil {
			for _, e := range userResp.Variables {
				rows = append(rows, systemKVRow{
					Scope:  "user",
					Name:   e.Name,
					Value:  e.Value,
					Secret: e.Secret,
				})
			}
		}
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Scope != rows[j].Scope {
			return rows[i].Scope < rows[j].Scope
		}
		return rows[i].Name < rows[j].Name
	})
	return rows, nil
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
