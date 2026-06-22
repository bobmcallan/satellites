package codemap

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// RenderJSON writes the map as stable, indented JSON (--json output, AC3).
func (m *Map) RenderJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(m)
}

// Render writes the human-readable map: a per-surface entry table followed by
// the orphan list (highest confidence first) with evidence.
func (m *Map) Render(w io.Writer) {
	var cli, server, mcp, hook int
	for _, e := range m.Entries {
		switch e.Surface {
		case SurfaceCLI:
			cli++
		case SurfaceServer:
			server++
		case SurfaceMCP:
			mcp++
		case SurfaceHook:
			hook++
		}
	}
	fmt.Fprintf(w, "code map — %d entry points (cli=%d server=%d mcp=%d hook=%d), %d orphan(s)\n\n",
		len(m.Entries), cli, server, mcp, hook, len(m.Orphans))

	cur := Surface("")
	for _, e := range m.Entries {
		if e.Surface != cur {
			cur = e.Surface
			fmt.Fprintf(w, "── %s ──\n", strings.ToUpper(string(cur)))
		}
		loc := e.Handler
		if e.File != "" {
			loc = fmt.Sprintf("%s (%s:%d)", e.Handler, e.File, e.Line)
		}
		status := invokerSummary(e)
		fmt.Fprintf(w, "  %-34s %-44s %s\n", e.Name, loc, status)
		if data := dataSummary(e); data != "" {
			fmt.Fprintf(w, "  %-34s %s\n", "", data)
		}
	}

	fmt.Fprintf(w, "\n── ORPHANS (no system invoker) ──\n")
	if len(m.Orphans) == 0 {
		fmt.Fprintln(w, "  none")
		return
	}
	for _, e := range m.Orphans {
		fmt.Fprintf(w, "  [%s] %s %s — %s\n", e.Confidence, e.Surface, e.Name, e.Note)
	}
}

func invokerSummary(e EntryPoint) string {
	if e.Surface == SurfaceMCP && e.Exposed {
		return "mcp-exposed"
	}
	if len(e.Invokers) == 0 {
		return "ORPHAN (no invoker)"
	}
	seen := map[string]bool{}
	var srcs []string
	for _, inv := range e.Invokers {
		if !seen[inv.Source] {
			seen[inv.Source] = true
			srcs = append(srcs, inv.Source)
		}
	}
	return "← " + strings.Join(srcs, ",")
}

func dataSummary(e EntryPoint) string {
	var parts []string
	if len(e.Produces) > 0 {
		parts = append(parts, "produces "+strings.Join(e.Produces, ","))
	}
	if len(e.Consumes) > 0 {
		parts = append(parts, "consumes "+strings.Join(e.Consumes, ","))
	}
	return strings.Join(parts, "  ")
}
