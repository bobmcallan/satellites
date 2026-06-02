// Package techdebt is the enforcement core of the broken-windows programme
// (principle:broken-windows, story sty_dd128ef6). It reconciles the set of
// currently-FAILING checks against a quarantine register of KNOWN, OWNED debt
// and decides whether a commit may proceed clean: a failure is yours to fix or
// file, never to pass by, and known debt only shrinks.
//
// It is a pure function over its inputs (the failing check ids + the parsed
// register), so it unit-tests without a DB and without running any suite — the
// CLI subcommand that wraps it supplies the failing set (parsed from `go test`
// output via ParseFailures) and the register (read via document_get, parsed via
// ParseRegister). It is READ-ONLY: it decides, it does not write.
package techdebt

import (
	"sort"
	"strings"
)

// Entry is one row of the quarantine register: a known-failing check, the
// story that owns it, and why it is tolerated for now. An Entry whose StoryID
// is empty is UNOWNED — debt nobody is on the hook for — which the gate refuses
// (you may not pad the register to dodge the gate; every window must be named
// and owned).
type Entry struct {
	CheckID string `json:"check_id"`
	StoryID string `json:"story_id"`
	Reason  string `json:"reason"`
}

// Result is the reconciled verdict for one commit. Every register row and every
// failing check lands in exactly one bucket.
type Result struct {
	// NewRed: failing ∧ not in the register. The broken windows — they BLOCK
	// until each is fixed or captured as an owned register row.
	NewRed []string `json:"new_red"`
	// Unowned: register rows with no story_id. Refused growth — they BLOCK
	// (this is AC3's "grows the register" refusal: a known window must name
	// its owning story).
	Unowned []Entry `json:"unowned"`
	// Stale: owned register rows whose check now passes. Closed windows — the
	// row MUST be removed (the ratchet; the register shrinks). Advisory, it
	// does not block, because a now-passing window is the good direction.
	Stale []Entry `json:"stale"`
	// Registered: failing ∧ owned register row. Tolerated debt — it passes.
	Registered []Entry `json:"registered"`
	// OK: the commit may proceed clean — no new red and no unowned entry.
	OK bool `json:"ok"`
}

// Reconcile overlays the failing-check set onto the register and classifies
// every check. `failing` is the set of check ids that failed this run;
// `register` is the known-debt rows. OK is true when there is no new red and no
// unowned register entry — stale rows are reported (the register should shrink)
// but never block, since a now-passing window is the direction we want.
func Reconcile(failing []string, register []Entry) Result {
	failingSet := make(map[string]bool, len(failing))
	for _, c := range failing {
		if c = strings.TrimSpace(c); c != "" {
			failingSet[c] = true
		}
	}

	registered := make(map[string]bool, len(register))
	var res Result
	for _, e := range register {
		e.CheckID = strings.TrimSpace(e.CheckID)
		if e.CheckID == "" {
			continue
		}
		registered[e.CheckID] = true
		switch {
		case strings.TrimSpace(e.StoryID) == "":
			res.Unowned = append(res.Unowned, e)
		case !failingSet[e.CheckID]:
			res.Stale = append(res.Stale, e)
		default:
			res.Registered = append(res.Registered, e)
		}
	}

	seenNew := make(map[string]bool)
	for _, c := range failing {
		c = strings.TrimSpace(c)
		if c == "" || registered[c] || seenNew[c] {
			continue
		}
		seenNew[c] = true
		res.NewRed = append(res.NewRed, c)
	}
	sort.Strings(res.NewRed)

	res.OK = len(res.NewRed) == 0 && len(res.Unowned) == 0
	return res
}

// ParseFailures extracts the failing check ids from `go test` output. It reads
// every `--- FAIL: <name>` line (top-level or indented subtest), reduces a
// subtest path to its top-level test name (the registerable unit), and returns
// the deduped set in first-seen order.
//
// Build/compile failures are deliberately NOT returned here: a broken build is
// never tolerable debt, so the caller treats a non-zero `go build` as an
// unconditional block rather than a registerable check.
func ParseFailures(testOutput string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(testOutput, "\n") {
		line = strings.TrimSpace(line)
		const marker = "--- FAIL:"
		i := strings.Index(line, marker)
		if i < 0 {
			continue
		}
		rest := strings.TrimSpace(line[i+len(marker):])
		// name is the first whitespace-delimited token (drops the "(0.00s)").
		name := rest
		if sp := strings.IndexAny(name, " \t"); sp >= 0 {
			name = name[:sp]
		}
		// Reduce a subtest path (Test/sub/sub) to its top-level test name.
		if slash := strings.IndexByte(name, '/'); slash >= 0 {
			name = name[:slash]
		}
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// ParseRegister reads the register document body and returns its rows. The
// register is authored as a markdown table with columns check_id | story_id |
// reason; ParseRegister reads every pipe-delimited data row, skipping the
// header (`check_id`) and the `---` separator. Prose around the table is
// ignored, so the document stays human-readable.
func ParseRegister(body string) []Entry {
	var out []Entry
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := splitRow(line)
		if len(cells) < 1 {
			continue
		}
		check := cells[0]
		if check == "" || strings.EqualFold(check, "check_id") || isSeparator(check) {
			continue
		}
		e := Entry{CheckID: check}
		if len(cells) > 1 {
			e.StoryID = cells[1]
		}
		if len(cells) > 2 {
			e.Reason = cells[2]
		}
		out = append(out, e)
	}
	return out
}

// splitRow splits a markdown table row on "|" and trims each cell, dropping the
// empty leading/trailing cells produced by the bounding pipes.
func splitRow(line string) []string {
	parts := strings.Split(line, "|")
	cells := make([]string, 0, len(parts))
	for _, p := range parts {
		cells = append(cells, strings.TrimSpace(p))
	}
	// Drop the empties created by the leading/trailing "|".
	for len(cells) > 0 && cells[0] == "" {
		cells = cells[1:]
	}
	for len(cells) > 0 && cells[len(cells)-1] == "" {
		cells = cells[:len(cells)-1]
	}
	return cells
}

// isSeparator reports whether a cell is a markdown table rule (---, :--:, etc.).
func isSeparator(cell string) bool {
	cell = strings.TrimSpace(cell)
	if cell == "" {
		return false
	}
	return strings.Trim(cell, "-: ") == ""
}
