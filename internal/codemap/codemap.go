// Package codemap builds a repo-agnostic reachability map: every entry point
// across the four surfaces (CLI command, server route, MCP verb, harness hook)
// joined to its system INVOKERS and the ledger-kind data it produces/consumes,
// with an ORPHAN list flagging entry points no process reaches whose data
// dead-ends in other un-invoked paths.
//
// It is the durable mechanism behind the one-shot reachability report
// (docs/reports/reachability-2026-06-23.md, sty_d739fc2f): the same
// entry-point → invoker → data trace, re-runnable so dead-feature drift (the
// `evidence` shape — a registered command with no system invoker whose data is
// read only by other un-invoked commands) is caught continuously.
//
// The deadness signal is SEMANTIC, not Go-unreachable: every registered command
// "reaches" from main, so `deadcode`/`unused` miss it. The map derives entry
// points from the REGISTRIES (the cobra tree, the verb catalog, the server mux,
// settings.json) — never a hardcoded command list (epic:enforcement-surface) —
// and classifies an entry point LIVE when any system invoker references it: a
// settings.json hook, a skill/.github/scripts mention, or an internal Go caller.
package codemap

import (
	"sort"
	"strings"
)

// Surface is the registry an entry point belongs to.
type Surface string

const (
	SurfaceCLI    Surface = "cli"    // a cobra command leaf
	SurfaceServer Surface = "server" // a server HTTP route
	SurfaceMCP    Surface = "mcp"    // a verb in the dispatch registry
	SurfaceHook   Surface = "hook"   // a harness hook wired in settings.json
)

// Confidence is how sure the orphan verdict is.
type Confidence string

const (
	ConfHigh   Confidence = "high"   // no invoker AND data dead-ends in un-invoked paths
	ConfMedium Confidence = "medium" // no invoker, but produces/consumes data a live path may read
	ConfLow    Confidence = "low"    // no invoker and no data edges (likely a utility, not dead)
)

// Invoker is a system reference to an entry point — the thing that makes it
// LIVE. Source is a short tag (settings.json, skill, github, script, go).
type Invoker struct {
	Source string `json:"source"`
	File   string `json:"file"`
	Line   int    `json:"line,omitempty"`
}

// EntryPoint is one reachable surface item plus its reachability evidence.
type EntryPoint struct {
	Surface Surface `json:"surface"`
	// Name is the surface-native identifier: the command path ("evidence ci"),
	// the route ("GET /stories/{id}"), the verb ("document_get"), or the hook
	// event + command ("Stop: hook stopcheck").
	Name string `json:"name"`
	// Handler is the resolved handler symbol (short name) and File:Line.
	Handler string `json:"handler,omitempty"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	// Exposed marks an MCP verb advertised on the transport (vs exec-only).
	Exposed bool `json:"exposed,omitempty"`
	// Invokers are the system references found. Empty == no system invoker.
	Invokers []Invoker `json:"invokers,omitempty"`
	// Produces / Consumes are the ledger kinds this entry point's handler
	// writes / reads (the data edges).
	Produces []string `json:"produces,omitempty"`
	Consumes []string `json:"consumes,omitempty"`
	// Orphan + Confidence + Note are the verdict (set by Classify).
	Orphan     bool       `json:"orphan"`
	Confidence Confidence `json:"confidence,omitempty"`
	Note       string     `json:"note,omitempty"`
}

// Live reports whether any system invoker references this entry point.
func (e EntryPoint) Live() bool { return len(e.Invokers) > 0 }

// Map is the full reachability result.
type Map struct {
	RepoRoot string       `json:"repo_root"`
	Entries  []EntryPoint `json:"entries"`
	// Orphans is the subset with Orphan == true, kept in a stable order.
	Orphans []EntryPoint `json:"orphans"`
	// ConsumedKinds is the set of ledger kinds read anywhere in the repo (set by
	// ScanDataEdges). A HIGH orphan must produce a kind that is in this set —
	// a write-only kind with no reader is not a dead cluster.
	ConsumedKinds map[string]bool `json:"-"`
}

// Classify fills Orphan/Confidence/Note for every entry and populates Orphans.
// An entry is an orphan when it has no system invoker. Hooks are never orphans
// (the harness fires them), and an MCP verb advertised on the transport
// (Exposed) is reachable by any MCP client, so it is never an orphan either.
//
// Confidence ranks the orphans by their data edges, reproducing the report's
// evidence-vs-utility split:
//   - HIGH   — produces a ledger kind whose every OTHER producer/consumer is
//     itself an un-invoked entry point (a wholly un-invoked cluster: `evidence`).
//   - MEDIUM — produces/consumes data, but a LIVE entry point also touches that
//     kind (a manual gate whose output still feeds a live reader: `surface`).
//   - LOW    — no data edges at all (a utility with no automation caller).
func (m *Map) Classify() {
	// kindLiveConsumerOrProducer[k] == true when some LIVE entry point produces
	// or consumes ledger kind k.
	liveTouch := map[string]bool{}
	for _, e := range m.Entries {
		if !e.Live() {
			continue
		}
		for _, k := range e.Produces {
			liveTouch[k] = true
		}
		for _, k := range e.Consumes {
			liveTouch[k] = true
		}
	}

	for i := range m.Entries {
		e := &m.Entries[i]
		e.Orphan = false
		e.Confidence = ""
		if e.Surface == SurfaceHook {
			continue // harness-fired
		}
		if e.Surface == SurfaceMCP && e.Exposed {
			e.Note = "reachable as an advertised MCP tool"
			continue
		}
		if e.Live() {
			continue
		}
		// No system invoker → orphan candidate. Rank by data edges.
		e.Orphan = true
		switch {
		case len(e.Produces) == 0 && len(e.Consumes) == 0:
			e.Confidence = ConfLow
			e.Note = "no system invoker and no ledger data edges — likely an operator/agent utility, not dead"
		case producesDeadCluster(e, liveTouch, m.ConsumedKinds):
			e.Confidence = ConfHigh
			e.Note = "no system invoker; it produces a ledger kind read only by other un-invoked paths (dead cluster)"
		default:
			e.Confidence = ConfMedium
			e.Note = "no system invoker, but its data is write-only or also touched by a live path — un-wired, not provably dead"
		}
	}

	m.bumpDeadFamilies()

	m.Orphans = m.Orphans[:0]
	for i := range m.Entries {
		if m.Entries[i].Orphan {
			m.Orphans = append(m.Orphans, m.Entries[i])
		}
	}

	sort.SliceStable(m.Orphans, func(a, b int) bool {
		ra, rb := confRank(m.Orphans[a].Confidence), confRank(m.Orphans[b].Confidence)
		if ra != rb {
			return ra < rb
		}
		return m.Orphans[a].Name < m.Orphans[b].Name
	})
}

// bumpDeadFamilies lifts a whole CLI command family to HIGH confidence when
// every command in it is an orphan AND at least one member's data dead-ends —
// the report's cluster verdict: `evidence ci` produces the dead `ci_result`, so
// its siblings (`evidence audit/review/show`, mere readers) are dead with it.
func (m *Map) bumpDeadFamilies() {
	families := map[string][]int{}  // family token → entry indices (CLI only)
	familyLive := map[string]bool{} // family has a non-orphan member
	familyDead := map[string]bool{} // family has a HIGH (dead-ending) member
	for i := range m.Entries {
		e := &m.Entries[i]
		if e.Surface != SurfaceCLI {
			continue
		}
		fam := strings.Fields(e.Name)[0]
		families[fam] = append(families[fam], i)
		if !e.Orphan {
			familyLive[fam] = true
		}
		if e.Orphan && e.Confidence == ConfHigh {
			familyDead[fam] = true
		}
	}
	for fam, idxs := range families {
		if familyLive[fam] || !familyDead[fam] {
			continue
		}
		for _, i := range idxs {
			e := &m.Entries[i]
			if e.Orphan && e.Confidence != ConfHigh {
				e.Confidence = ConfHigh
				e.Note = "no system invoker; member of a wholly un-invoked command family whose data dead-ends (dead cluster)"
			}
		}
	}
}

// producesDeadCluster is true when the entry produces a ledger kind that is
// (a) actually consumed somewhere in the repo, yet (b) touched by no LIVE entry
// point — i.e. a real producer→consumer cluster wholly inside un-invoked paths
// (the `evidence`/`ci_result` shape). A write-only kind (no consumer) or a kind
// a live path also reads does not qualify.
func producesDeadCluster(e *EntryPoint, liveTouch, consumed map[string]bool) bool {
	for _, k := range e.Produces {
		if consumed[k] && !liveTouch[k] {
			return true
		}
	}
	return false
}

func confRank(c Confidence) int {
	switch c {
	case ConfHigh:
		return 0
	case ConfMedium:
		return 1
	case ConfLow:
		return 2
	}
	return 3
}

// SortEntries orders entries by surface then name for stable output.
func (m *Map) SortEntries() {
	order := map[Surface]int{SurfaceHook: 0, SurfaceCLI: 1, SurfaceServer: 2, SurfaceMCP: 3}
	sort.SliceStable(m.Entries, func(a, b int) bool {
		sa, sb := order[m.Entries[a].Surface], order[m.Entries[b].Surface]
		if sa != sb {
			return sa < sb
		}
		return m.Entries[a].Name < m.Entries[b].Name
	})
}
