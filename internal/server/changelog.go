package server

import (
	"bytes"
	"html/template"
	"net/http"
	"sort"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"

	"github.com/bobmcallan/satellites/internal/changelog"
)

// changelogStore is wired by the server boot path. Unwired callers
// (some unit tests) get a "not configured" 500 from the handler.
var changelogStore *changelog.Store

// SetChangelogStore is called from main once the DB is open. Keeping
// the wire-up here rather than threading the store through Config
// matches the verb-package pattern (SetAuthStore, SetProjectStore).
func SetChangelogStore(s *changelog.Store) { changelogStore = s }

// markdownRenderer is the per-process goldmark instance used for
// rendering changelog entry content. WithUnsafe is intentionally
// absent — goldmark refuses to emit raw HTML by default, so any
// <script> the operator inserts in a content body lands as escaped
// text. The renderer is safe to use concurrently.
var markdownRenderer = goldmark.New(
	goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	goldmark.WithRendererOptions(html.WithHardWraps()),
)

var changelogTmpl = template.Must(template.ParseFS(assets,
	"templates/changelog.html", "templates/_user_menu.html"))

// changelogGroup buckets changelog.Entry rows by service. Internal to
// the grouping step — the template renders the view variant below.
type changelogGroup struct {
	Service string
	Entries []changelog.Entry
}

// changelogGroupView is the render-time shape: rows already pass
// through markdown rendering, dates formatted as YYYY-MM-DD.
type changelogGroupView struct {
	Service string
	Entries []changelogEntryView
}

// changelogEntryView wraps a changelog.Entry with the rendered HTML
// body. Content is rendered through goldmark; the result is wrapped
// in template.HTML so the template emits it without a second escape
// pass.
type changelogEntryView struct {
	ID            string
	VersionFrom   string
	VersionTo     string
	EffectiveDate string
	CreatedAt     string
	ContentHTML   template.HTML
}

type changelogPageData struct {
	Title       string
	Groups      []changelogGroupView
	UserEmail   string
	UserName    string
	UserAvatar  string
	ActiveNav   string
	FooterName  string
	FooterEmail string
	Version     string
}

// changelogHandler serves GET /changelog. Public — no auth required;
// the user menu degrades to a sign-in chip when no session cookie is
// present. Listing fetches the first page (200 max) and groups by
// service before rendering.
func changelogHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/changelog" {
			http.NotFound(w, r)
			return
		}
		if changelogStore == nil {
			http.Error(w, "changelog store not configured", http.StatusInternalServerError)
			return
		}

		res, err := changelogStore.List(r.Context(), changelog.ListOptions{Limit: 200})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		groups := groupByService(res.Items)
		views := make([]changelogGroupView, 0, len(groups))
		for _, g := range groups {
			ev := make([]changelogEntryView, 0, len(g.Entries))
			for _, e := range g.Entries {
				v := changelogEntryView{
					ID:          e.ID,
					VersionFrom: e.VersionFrom,
					VersionTo:   e.VersionTo,
					CreatedAt:   e.CreatedAt.UTC().Format("2006-01-02"),
					ContentHTML: renderMarkdown(e.Content),
				}
				if e.EffectiveDate != nil {
					v.EffectiveDate = e.EffectiveDate.UTC().Format("2006-01-02")
				}
				ev = append(ev, v)
			}
			views = append(views, changelogGroupView{Service: g.Service, Entries: ev})
		}

		data := changelogPageData{
			Title:       "changelog · satellites",
			Groups:      views,
			ActiveNav:   "changelog",
			FooterName:  footerName,
			FooterEmail: footerEmail,
			Version:     versionString(),
		}
		// Best-effort user resolution for the nav avatar — same pattern
		// as indexHandler.
		if cfg.Sessions != nil {
			if userID, err := cfg.Sessions.UserID(r); err == nil && cfg.Store != nil && cfg.Store.DB != nil {
				if u, err := cfg.Store.GetUserByID(r.Context(), userID); err == nil && u != nil {
					data.UserEmail = u.Email
					data.UserName = u.DisplayName
					data.UserAvatar = avatarLetter(u.DisplayName, u.Email)
				}
			}
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := changelogTmpl.Execute(w, data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// groupByService partitions entries into service buckets. Service
// order follows first-appearance in the input slice (which the store
// already orders by effective_date / created_at), so the busiest
// service surfaces first on the page.
func groupByService(entries []changelog.Entry) []changelogGroup {
	byService := map[string]*changelogGroup{}
	order := []string{}
	for _, e := range entries {
		g, ok := byService[e.Service]
		if !ok {
			g = &changelogGroup{Service: e.Service}
			byService[e.Service] = g
			order = append(order, e.Service)
		}
		g.Entries = append(g.Entries, e)
	}
	out := make([]changelogGroup, 0, len(order))
	for _, name := range order {
		g := byService[name]
		// Stable secondary sort within a group — preserves store order
		// for ties but keeps NULL-effective_date rows last.
		sort.SliceStable(g.Entries, func(i, j int) bool {
			return effectiveDateAfter(g.Entries[i], g.Entries[j])
		})
		out = append(out, *g)
	}
	return out
}

func effectiveDateAfter(a, b changelog.Entry) bool {
	switch {
	case a.EffectiveDate != nil && b.EffectiveDate != nil:
		return a.EffectiveDate.After(*b.EffectiveDate)
	case a.EffectiveDate != nil && b.EffectiveDate == nil:
		return true
	case a.EffectiveDate == nil && b.EffectiveDate != nil:
		return false
	default:
		return a.CreatedAt.After(b.CreatedAt)
	}
}

// renderMarkdown converts the supplied markdown to safe HTML. goldmark
// is configured without WithUnsafe, so raw HTML and dangerous URL
// schemes are escaped at the renderer layer. Errors fall back to an
// empty body so a malformed entry doesn't break the page.
func renderMarkdown(md string) template.HTML {
	var buf bytes.Buffer
	if err := markdownRenderer.Convert([]byte(md), &buf); err != nil {
		return ""
	}
	return template.HTML(buf.String())
}
