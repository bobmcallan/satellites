package cli

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/bobmcallan/satellites/internal/verb"
)

// sty_d7698c22: the live command surface is enumerated without cobra's
// auto-generated help/completion noise.
func TestLiveCommandNames_ExcludesAutogen(t *testing.T) {
	names := liveCommandNames(NewRootCmd())
	if len(names) == 0 {
		t.Fatal("no live commands enumerated")
	}
	for _, n := range names {
		if n == "help" || n == "completion" {
			t.Errorf("auto-generated command %q should be excluded", n)
		}
	}
	// Real commands are present.
	if !contains(names, "update") || !contains(names, "version") {
		t.Errorf("expected real commands in surface, got %v", names)
	}
}

// AC2: a command absent from the doc is reported as drift.
func TestSurfaceDrift_FlagsUndocumented(t *testing.T) {
	doc := "We support `satellites update` and `satellites version`."
	missing := surfaceDrift([]string{"update", "version", "techdebt"}, doc)
	if len(missing) != 1 || missing[0] != "techdebt" {
		t.Fatalf("expected only techdebt missing, got %v", missing)
	}
}

// AC3: once every command is named in the doc, there is no drift.
func TestSurfaceDrift_CleanWhenAllDocumented(t *testing.T) {
	doc := "Commands: update, version, techdebt, surface."
	missing := surfaceDrift([]string{"update", "version", "techdebt", "surface"}, doc)
	if len(missing) != 0 {
		t.Fatalf("expected no drift, got %v", missing)
	}
}

// A command name embedded in a brace-list (e.g. `{document,skill}`)
// counts as documented — whole-word matching spans non-word delimiters.
func TestSurfaceDrift_MatchesInsideBraceList(t *testing.T) {
	doc := "satellites {document,skill,principle} upload"
	missing := surfaceDrift([]string{"document", "skill", "principle"}, doc)
	if len(missing) != 0 {
		t.Fatalf("brace-list commands should match, got missing %v", missing)
	}
}

// A longer command must not be matched by a shorter substring: "doc"
// should not be considered documented just because "document" appears.
func TestSurfaceDrift_WholeWordOnly(t *testing.T) {
	doc := "satellites document upload"
	missing := surfaceDrift([]string{"doc"}, doc)
	if len(missing) != 1 {
		t.Fatalf("substring should not count as documented, got %v", missing)
	}
}

// sty_1bdebdd9 / AC2+AC4: an ABSENT command-surface doc (present=false, empty
// body) must NOT crash the gate. evaluateSurface degrades to "every live
// command undocumented" — actionable drift — and returns a drift error with a
// BLOCKED verdict, never a read/crash error. No claude, no network.
func TestEvaluateSurface_MissingDocReportsDriftNoCrash(t *testing.T) {
	live := []string{"surface", "update", "version"}
	var buf bytes.Buffer
	var gotVerdict string
	var gotBlocking int
	err := evaluateSurface(&buf, live, "", false, "client-command-surface", func(v string, n int) {
		gotVerdict, gotBlocking = v, n
	})
	if err == nil {
		t.Fatal("missing doc should yield a drift error, got nil")
	}
	if gotVerdict != gateVerdictBlocked {
		t.Errorf("expected BLOCKED verdict, got %q", gotVerdict)
	}
	if gotBlocking != len(live) {
		t.Errorf("expected all %d live commands undocumented, got %d", len(live), gotBlocking)
	}
	out := buf.String()
	if !strings.Contains(out, "absent or empty") {
		t.Errorf("report should name the absent doc, got:\n%s", out)
	}
	for _, c := range live {
		if !strings.Contains(out, c) {
			t.Errorf("report should list undocumented command %q, got:\n%s", c, out)
		}
	}
}

// A present, fully-documenting doc accepts cleanly: present=true, no drift,
// CLEAN verdict, nil error — the absent-doc degradation does not leak into the
// happy path.
func TestEvaluateSurface_PresentDocCleanAccepts(t *testing.T) {
	live := []string{"surface", "version"}
	var buf bytes.Buffer
	var gotVerdict string
	err := evaluateSurface(&buf, live, "commands: surface, version", true, "client-command-surface", func(v string, n int) {
		gotVerdict = v
	})
	if err != nil {
		t.Fatalf("fully documented surface should accept, got %v", err)
	}
	if gotVerdict != gateVerdictClean {
		t.Errorf("expected CLEAN verdict, got %q", gotVerdict)
	}
	if strings.Contains(buf.String(), "absent or empty") {
		t.Errorf("present doc should not print the absent-doc notice, got:\n%s", buf.String())
	}
}

// isDocNotFound classifies a document_get not-found (in-process sentinel and the
// HTTP error string) as absent, while a genuine transport error is NOT absent —
// so only the no-doc path degrades; real failures still surface.
func TestIsDocNotFound(t *testing.T) {
	if !isDocNotFound(fmt.Errorf("document_get: %w", verb.ErrNotFound)) {
		t.Error("in-process verb.ErrNotFound should be classified as not-found")
	}
	if !isDocNotFound(fmt.Errorf("cli: document_get: document_get: verb: not found: project/client-command-surface")) {
		t.Error("HTTP not-found error string should be classified as not-found")
	}
	if isDocNotFound(fmt.Errorf("cli: dispatch document_get: connection refused")) {
		t.Error("a transport error must NOT be classified as not-found")
	}
}
