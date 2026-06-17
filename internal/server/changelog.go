package server

import (
	"bytes"
	"encoding/json"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/parser"

	"github.com/bobmcallan/satellites/internal/verb"
)

// changelogDocType is the documents.type discriminator for changelog rows. The
// server reaches the substrate through verb dispatch, not the document domain
// package (the layering guard), so the type rides as a literal here.
const changelogDocType = "changelog"

// changelogListCap bounds the single-page /changelog listing. Changelog entries
// are manual release notes (low volume); the cursor pagination the dedicated
// table once carried is dropped with the table — one capped page is enough
// (epic:satellites-backbone, Workstream C). Raise this if a corpus ever exceeds
// it (the page would silently truncate, never error).
const changelogListCap = 200

// markdownRenderer is the per-process goldmark instance used for rendering
// changelog entry content (and, via renderStoryMarkdown, story/project bodies).
// WithUnsafe is intentionally absent — goldmark refuses to emit raw HTML by
// default, so any <script> in a body lands as escaped text. Safe to use
// concurrently.
var markdownRenderer = goldmark.New(
	goldmark.WithParserOptions(parser.WithAutoHeadingID()),
)

var changelogTmpl = template.Must(template.ParseFS(assets,
	"templates/changelog.html", "templates/_user_menu.html"))

// clEntry is the changelog row as reconstructed from a type:changelog document —
// the typed fields (service / version_from / version_to / effective_date) ride
// as tags, the content is the document's latest body. It replaces the retired
// changelog.Entry now that changelog lives in the documents store.
type clEntry struct {
	ID            string
	Service       string
	VersionFrom   string
	VersionTo     string
	Content       string
	CreatedAt     time.Time
	EffectiveDate *time.Time
}

// changelogGroup buckets clEntry rows by service.
type changelogGroup struct {
	Service string
	Entries []clEntry
}

// changelogGroupView is the render-time shape: rows already pass through
// markdown rendering, dates formatted as YYYY-MM-DD.
type changelogGroupView struct {
	Service string
	Entries []changelogEntryView
}

// changelogEntryView wraps a clEntry with the rendered HTML body.
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

// changelogHandler serves GET /changelog. Public — no auth required. It reads
// type:changelog documents from the generic substrate (document_list +
// document_get for bodies — the /library pattern), maps each back to a clEntry,
// and groups by service before rendering. The dedicated changelog table + verbs
// are retired (epic:satellites-backbone, Workstream C).
func changelogHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/changelog" {
			http.NotFound(w, r)
			return
		}
		ctx := r.Context()

		listBody, _ := json.Marshal(verb.DocumentListRequest{
			Type:  changelogDocType,
			Scope: "system",
			Limit: changelogListCap,
		})
		raw, err := verb.Dispatch(ctx, "document_list", listBody)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var lr verb.DocumentListResponse
		if err := json.Unmarshal(raw, &lr); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		entries := make([]clEntry, 0, len(lr.Items))
		for _, it := range lr.Items {
			e := clEntry{
				ID:          it.ID,
				Service:     tagValue(it.Tags, "service:"),
				VersionFrom: tagValue(it.Tags, "version_from:"),
				VersionTo:   tagValue(it.Tags, "version_to:"),
				CreatedAt:   it.CreatedAt,
			}
			if d := tagValue(it.Tags, "effective_date:"); d != "" {
				if t, perr := time.Parse("2006-01-02", d); perr == nil {
					e.EffectiveDate = &t
				}
			}
			// Body via document_get by id (the list view carries no body; id
			// always resolves, unlike a type-scoped name lookup).
			getBody, _ := json.Marshal(verb.DocumentGetRequest{ID: it.ID})
			if graw, gerr := verb.Dispatch(ctx, "document_get", getBody); gerr == nil {
				var gr verb.DocumentGetResponse
				if json.Unmarshal(graw, &gr) == nil {
					e.Content = gr.RawBody
				}
			}
			entries = append(entries, e)
		}

		groups := groupByService(entries)
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

// tagValue returns the suffix of the first tag with the given "key:" prefix, or
// "" when none is present.
func tagValue(tags []string, prefix string) string {
	for _, t := range tags {
		if strings.HasPrefix(t, prefix) {
			return strings.TrimPrefix(t, prefix)
		}
	}
	return ""
}

// groupByService partitions entries into service buckets, first-appearance
// order, with effective_date DESC within a group (NULLs last).
func groupByService(entries []clEntry) []changelogGroup {
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
		sort.SliceStable(g.Entries, func(i, j int) bool {
			return effectiveDateAfter(g.Entries[i], g.Entries[j])
		})
		out = append(out, *g)
	}
	return out
}

func effectiveDateAfter(a, b clEntry) bool {
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

// renderMarkdown converts markdown to safe HTML (goldmark without WithUnsafe, so
// raw HTML and dangerous URL schemes are escaped). Errors fall back to an empty
// body so a malformed entry can't break the page. Shared by the story/project
// detail renderers via renderStoryMarkdown.
func renderMarkdown(md string) template.HTML {
	var buf bytes.Buffer
	if err := markdownRenderer.Convert([]byte(md), &buf); err != nil {
		return ""
	}
	return template.HTML(buf.String())
}

// unescapeLiteralNewlines repairs a body whose newlines/tabs were stored as
// literal backslash escape sequences (sty_0633bcf5). No-op when none present.
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
// literal-escape-sequence newlines so escaped bodies read the same as clean ones.
func renderStoryMarkdown(md string) template.HTML {
	return renderMarkdown(unescapeLiteralNewlines(md))
}
