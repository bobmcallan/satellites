package server

import (
	"sort"
	"strconv"
	"strings"
)

// story_filter.go — the SERVER-SIDE port of the story-panel query grammar
// (sty_1bd0098a). The panel's filter/sort used to live only in
// story_panel.js, applied to the rows already rendered on the current page,
// so it could not compose with server-side pagination: paginating dropped the
// filter, a match on another page was unreachable, and multi-token free text
// matched a literal phrase. Pushing the same grammar into the server query
// makes dispatchStoryList paginate the filtered, sorted set — page/total
// reflect the filter, prev/next carry it, and every match is reachable.
//
// The grammar mirrors story_panel.js exactly so the client (chip rendering +
// instant same-page preview) and the server (authoritative filter) never
// disagree:
//
//	status:<v[,v...]>    all|open|<status>  (open = not a terminal status;
//	                     terminal = done/cancelled for stories, complete/cancelled
//	                     for tasks — see isTerminalStatus, shared by both rows)
//	priority:<v[,v...]>  all|<priority>
//	category:<v[,v...]>  all|<category>
//	tags:<v>             single tag; multiple tokens OR
//	order:<field>        updated|created|priority|status|title|id|<tag-prefix>
//	<free text>          lowercased substring; multiple tokens OR (union)

// storyQuery is the parsed panel query.
type storyQuery struct {
	status   []string
	priority []string
	category []string
	tags     []string // OR across tokens
	order    string
	free     []string // OR across tokens (substring of id+title+tags)
}

// isEmpty reports whether the query constrains nothing — the handler keeps
// the cheaper cursor pagination path when there is no active filter.
func (q storyQuery) isEmpty() bool {
	return len(q.status) == 0 && len(q.priority) == 0 && len(q.category) == 0 &&
		len(q.tags) == 0 && q.order == "" && len(q.free) == 0
}

// orderFields are the row columns order:<field> can sort by directly; any
// other value is treated as a tag prefix (e.g. order:epic-order), matching
// story_panel.js's applyStoryOrder.
var orderFields = map[string]bool{
	"updated": true, "created": true, "priority": true,
	"status": true, "title": true, "id": true,
}

// parseStoryQuery ports story_panel.js parseStoryQuery: split on whitespace,
// route key:value tokens into structured buckets, everything else (including
// an unrecognised key:value) into free text.
func parseStoryQuery(s string) storyQuery {
	var q storyQuery
	for _, p := range strings.Fields(s) {
		if idx := strings.Index(p, ":"); idx > 0 {
			k := strings.ToLower(p[:idx])
			v := strings.ToLower(p[idx+1:])
			switch k {
			case "status", "priority", "category":
				for _, vv := range strings.Split(v, ",") {
					if vv == "" {
						continue
					}
					switch k {
					case "status":
						q.status = append(q.status, vv)
					case "priority":
						q.priority = append(q.priority, vv)
					case "category":
						q.category = append(q.category, vv)
					}
				}
				continue
			case "tags", "tag":
				if v != "" {
					q.tags = append(q.tags, v)
				}
				continue
			case "order":
				if v != "" {
					q.order = v
				}
				continue
			}
			// Unknown key:value falls through to free text — mirrors the JS.
		}
		q.free = append(q.free, strings.ToLower(p))
	}
	return q
}

// isTerminalStatus reports whether a row's status is a terminal (non-open) one.
// The story lifecycle ends at done/cancelled; the task lifecycle ends at
// complete/cancelled — both share this predicate, so the union is terminal. A
// status the other type never carries is harmless to the type that doesn't use
// it (no story is "complete"; no task is "done").
func isTerminalStatus(st string) bool {
	switch st {
	case "done", "cancelled", "canceled", "complete":
		return true
	}
	return false
}

func sliceContains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// matchFields is the ONE filter predicate, the server port of matchesRow (minus
// the expanded-row special case, which is presentation-only). It works on the
// primitive row fields so the SAME grammar gates both stories and tasks
// (sty_80447ada) — no duplicated filter logic. Free text uses OR (union)
// semantics: the row matches when its haystack contains ANY token.
func matchFields(id, title, status, priority, category string, tags []string, q storyQuery) bool {
	st := strings.ToLower(status)
	switch {
	case len(q.status) == 0:
		// Default: hide terminal rows.
		if isTerminalStatus(st) {
			return false
		}
	case sliceContains(q.status, "all"):
		// no status filter
	default:
		ok := false
		for _, want := range q.status {
			if want == "open" {
				if !isTerminalStatus(st) {
					ok = true
					break
				}
			} else if st == want {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if len(q.priority) > 0 && !sliceContains(q.priority, "all") {
		if !sliceContains(q.priority, strings.ToLower(priority)) {
			return false
		}
	}
	if len(q.category) > 0 && !sliceContains(q.category, "all") {
		if !sliceContains(q.category, strings.ToLower(category)) {
			return false
		}
	}
	if len(q.tags) > 0 {
		rowTags := " " + strings.ToLower(strings.Join(tags, " ")) + " "
		ok := false
		for _, t := range q.tags {
			if strings.Contains(rowTags, " "+t+" ") {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if len(q.free) > 0 {
		hay := strings.ToLower(id + " " + title + " " + strings.Join(tags, " "))
		ok := false
		for _, t := range q.free {
			if t != "" && strings.Contains(hay, t) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// matchStory / matchTask adapt the shared predicate to each row type.
func matchStory(s storyRow, q storyQuery) bool {
	return matchFields(s.ID, s.Title, s.Status, s.Priority, s.Category, s.Tags, q)
}

func matchTask(t taskRow, q storyQuery) bool {
	return matchFields(t.ID, t.Title, t.Status, t.Priority, t.Category, t.Tags, q)
}

// filterStories returns the subset of rows matching q, order preserved.
func filterStories(rows []storyRow, q storyQuery) []storyRow {
	out := make([]storyRow, 0, len(rows))
	for _, r := range rows {
		if matchStory(r, q) {
			out = append(out, r)
		}
	}
	return out
}

// filterTasks returns the subset of task rows matching q, order preserved —
// the same predicate as filterStories (sty_80447ada).
func filterTasks(rows []taskRow, q storyQuery) []taskRow {
	out := make([]taskRow, 0, len(rows))
	for _, r := range rows {
		if matchTask(r, q) {
			out = append(out, r)
		}
	}
	return out
}

// tagOrderValue pulls the value out of a `<prefix>:<v>` tag, or "" when the
// tag is absent (the row then sinks to the bottom of an ascending sort).
func tagOrderValue(s storyRow, prefix string) string {
	for _, t := range s.Tags {
		tl := strings.ToLower(t)
		if strings.HasPrefix(tl, prefix+":") {
			return tl[len(prefix)+1:]
		}
	}
	return ""
}

// naturalLess orders so that 1 < 2 < 10 < a < b: numeric values sort
// numerically and ahead of non-numeric ones (localeCompare numeric:true
// parity for the values projects actually use — epic-order:N, order:a).
func naturalLess(a, b string) bool {
	ai, aerr := strconv.Atoi(a)
	bi, berr := strconv.Atoi(b)
	switch {
	case aerr == nil && berr == nil:
		return ai < bi
	case aerr == nil:
		return true // numbers before letters
	case berr == nil:
		return false
	default:
		return a < b
	}
}

// sortStories reorders rows by order:<field>, ported from applyStoryOrder.
// An empty order leaves the server-rendered order (document_list default:
// created_at DESC) untouched. The sort is stable so equal keys keep their
// incoming order.
func sortStories(rows []storyRow, order string) {
	if order == "" {
		return
	}
	if orderFields[order] {
		sort.SliceStable(rows, func(i, j int) bool {
			a, b := rows[i], rows[j]
			switch order {
			case "title":
				// Title reads naturally ascending; the rest descend.
				return strings.ToLower(a.Title) < strings.ToLower(b.Title)
			case "id":
				return strings.ToLower(a.ID) > strings.ToLower(b.ID)
			case "status":
				return strings.ToLower(a.Status) > strings.ToLower(b.Status)
			case "priority":
				return strings.ToLower(a.Priority) > strings.ToLower(b.Priority)
			case "created":
				return a.CreatedAt.After(b.CreatedAt)
			case "updated":
				return a.UpdatedAt.After(b.UpdatedAt)
			}
			return false
		})
		return
	}
	// Tag-prefix sort (e.g. order:epic-order): natural ascending, rows
	// missing the tag sink to the bottom.
	sort.SliceStable(rows, func(i, j int) bool {
		av := tagOrderValue(rows[i], order)
		bv := tagOrderValue(rows[j], order)
		if av == "" && bv == "" {
			return false
		}
		if av == "" {
			return false // a missing → sinks below b
		}
		if bv == "" {
			return true // b missing → a rises above b
		}
		return naturalLess(av, bv)
	})
}
