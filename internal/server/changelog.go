package server

import (
	"bytes"
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"

	"github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/changelog"
	"github.com/bobmcallan/satellites/internal/verb"
)

var changelogPaginatorKeys = paginatorKeys{
	Page: "changelog_page", Cursor: "changelog_cursor", Back: "changelog_back",
}

const (
	changelogPageSizeFallback = 20
	changelogPageSizeMax      = 200
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
	Paginator   paginatorData
	UserEmail   string
	UserName    string
	UserAvatar  string
	ActiveNav   string
	FooterName  string
	FooterEmail string
	Version     string
}

// readChangelogPageSize mirrors readStoryPageSize: pulls the
// operator-tuned page size from the system KV via variable_get. Falls
// back to the const on miss / decode failure / non-numeric value with
// a WARN log so the misconfiguration is visible.
func readChangelogPageSize(ctx context.Context) int {
	body, _ := json.Marshal(verb.VariableGetRequest{
		Name:  "changelog.page_size",
		Scope: "system",
	})
	raw, err := verb.Dispatch(ctx, "variable_get", body)
	if err != nil {
		arbor.WarnCtx(ctx, "changelog page_size: variable_get failed, using fallback",
			"fallback", changelogPageSizeFallback, "err", err)
		return changelogPageSizeFallback
	}
	var resp verb.VariableGetResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		arbor.WarnCtx(ctx, "changelog page_size: decode failed, using fallback", "err", err)
		return changelogPageSizeFallback
	}
	n, err := strconv.Atoi(strings.TrimSpace(resp.Value))
	if err != nil || n < 1 {
		arbor.WarnCtx(ctx, "changelog page_size: non-numeric KV value, using fallback",
			"value", resp.Value, "fallback", changelogPageSizeFallback)
		return changelogPageSizeFallback
	}
	if n > changelogPageSizeMax {
		return changelogPageSizeMax
	}
	return n
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

		ctx := r.Context()
		q := r.URL.Query()
		pageSize := readChangelogPageSize(ctx)
		cursor := q.Get(changelogPaginatorKeys.Cursor)
		page := 1
		if p := strings.TrimSpace(q.Get(changelogPaginatorKeys.Page)); p != "" {
			if n, err := strconv.Atoi(p); err == nil && n > 0 {
				page = n
			}
		}
		backStack := readBackStack(q, changelogPaginatorKeys)

		res, err := changelogStore.List(ctx, changelog.ListOptions{
			Cursor: cursor,
			Limit:  pageSize,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		paginator := paginatorData{
			Page:     page,
			HasPrev:  len(backStack) > 0,
			HasNext:  res.NextCursor != "",
			PageSize: pageSize,
		}
		if paginator.HasPrev {
			paginator.PrevURL = prevPageURL("/changelog", page, backStack, changelogPaginatorKeys)
		}
		if paginator.HasNext {
			paginator.NextURL = nextPageURL("/changelog", page, cursor, res.NextCursor, backStack, changelogPaginatorKeys)
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
			Paginator:   paginator,
			ActiveNav:   "changelog",
			FooterName:  footerName,
			FooterEmail: footerEmail,
			Version:     versionString(),
		}
		// Best-effort user resolution for the nav avatar — same pattern
		// as indexHandler.
		if cfg.Sessions != nil {
			if userID, err := cfg.Sessions.UserID(r); err == nil && cfg.Store != nil && cfg.Store.DB != nil {
				if u, err := cfg.Store.GetUserByID(ctx, userID); err == nil && u != nil {
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

// unescapeLiteralNewlines repairs a body whose newlines/tabs were stored as
// literal backslash escape sequences (e.g. a caller that JSON-escaped the body,
// so it reaches the store as the two characters backslash-n rather than a
// newline — sty_0633bcf5). Without this, markdown headings/lists never form and
// the body renders as one run-on line. No-op when no escape sequence is present,
// so a clean body is untouched.
func unescapeLiteralNewlines(s string) string {
	if !strings.Contains(s, `\n`) && !strings.Contains(s, `\t`) {
		return s
	}
	s = strings.ReplaceAll(s, `\r\n`, "\n")
	s = strings.ReplaceAll(s, `\n`, "\n")
	s = strings.ReplaceAll(s, `\t`, "\t")
	return s
}

// renderStoryMarkdown renders a story body/ACs to safe HTML, first repairing
// literal-escape-sequence newlines (sty_0633bcf5) so escaped bodies read the
// same as clean ones.
func renderStoryMarkdown(md string) template.HTML {
	return renderMarkdown(unescapeLiteralNewlines(md))
}
