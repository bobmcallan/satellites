package techdebt

import (
	"reflect"
	"testing"
)

func checkIDs(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.CheckID
	}
	return out
}

// AC6: a failing check that is NOT in the register is a new red — it blocks.
func TestReconcile_NewRedBlocks(t *testing.T) {
	res := Reconcile([]string{"TestFreshlyBroken"}, nil)
	if res.OK {
		t.Fatalf("OK = true; a new uncovered red must block")
	}
	if !reflect.DeepEqual(res.NewRed, []string{"TestFreshlyBroken"}) {
		t.Fatalf("NewRed = %v; want [TestFreshlyBroken]", res.NewRed)
	}
	if len(res.Registered) != 0 || len(res.Unowned) != 0 || len(res.Stale) != 0 {
		t.Fatalf("unexpected buckets: %+v", res)
	}
}

// AC6: a failing check that IS in the register, owned by a story, passes.
func TestReconcile_RegisteredRedPasses(t *testing.T) {
	reg := []Entry{{CheckID: "TestKnownFlaky", StoryID: "sty_owner", Reason: "WSL flaky"}}
	res := Reconcile([]string{"TestKnownFlaky"}, reg)
	if !res.OK {
		t.Fatalf("OK = false; an owned registered red must pass. %+v", res)
	}
	if got := checkIDs(res.Registered); !reflect.DeepEqual(got, []string{"TestKnownFlaky"}) {
		t.Fatalf("Registered = %v; want [TestKnownFlaky]", got)
	}
	if len(res.NewRed) != 0 || len(res.Stale) != 0 || len(res.Unowned) != 0 {
		t.Fatalf("unexpected buckets: %+v", res)
	}
}

// AC6: growing the register with an UNOWNED entry (no story_id) is refused —
// you may not pad the register to dodge the gate.
func TestReconcile_RegisterGrowthUnownedRefused(t *testing.T) {
	reg := []Entry{{CheckID: "TestSneakedIn", StoryID: "", Reason: "no owner"}}
	res := Reconcile([]string{"TestSneakedIn"}, reg)
	if res.OK {
		t.Fatalf("OK = true; an unowned register entry must block")
	}
	if got := checkIDs(res.Unowned); !reflect.DeepEqual(got, []string{"TestSneakedIn"}) {
		t.Fatalf("Unowned = %v; want [TestSneakedIn]", got)
	}
	// An unowned entry is NOT also a new red — it is classified as the register
	// row it is, just a refused one.
	if len(res.NewRed) != 0 {
		t.Fatalf("NewRed = %v; an unowned register row should not double as a new red", res.NewRed)
	}
}

// AC6: shrinking the register is always allowed — a now-passing owned window is
// reported as stale (must be removed) and does not block.
func TestReconcile_RegisterShrinkAllowed(t *testing.T) {
	reg := []Entry{{CheckID: "TestNowFixed", StoryID: "sty_owner", Reason: "was broken"}}
	res := Reconcile(nil, reg) // nothing fails now
	if !res.OK {
		t.Fatalf("OK = false; a closed window (stale row) must not block. %+v", res)
	}
	if got := checkIDs(res.Stale); !reflect.DeepEqual(got, []string{"TestNowFixed"}) {
		t.Fatalf("Stale = %v; want [TestNowFixed]", got)
	}
	if len(res.NewRed) != 0 || len(res.Registered) != 0 || len(res.Unowned) != 0 {
		t.Fatalf("unexpected buckets: %+v", res)
	}
}

// A mixed run: one owned red passes, one fresh red blocks, one closed window is
// stale. The fresh red dominates the verdict.
func TestReconcile_Mixed(t *testing.T) {
	reg := []Entry{
		{CheckID: "TestKnown", StoryID: "sty_a", Reason: "owned"},
		{CheckID: "TestClosed", StoryID: "sty_b", Reason: "fixed"},
	}
	res := Reconcile([]string{"TestKnown", "TestBrandNew"}, reg)
	if res.OK {
		t.Fatalf("OK = true; a fresh red in the mix must block")
	}
	if !reflect.DeepEqual(res.NewRed, []string{"TestBrandNew"}) {
		t.Fatalf("NewRed = %v; want [TestBrandNew]", res.NewRed)
	}
	if got := checkIDs(res.Registered); !reflect.DeepEqual(got, []string{"TestKnown"}) {
		t.Fatalf("Registered = %v; want [TestKnown]", got)
	}
	if got := checkIDs(res.Stale); !reflect.DeepEqual(got, []string{"TestClosed"}) {
		t.Fatalf("Stale = %v; want [TestClosed]", got)
	}
}

// A fully clean tree with an empty register is OK.
func TestReconcile_CleanEmpty(t *testing.T) {
	res := Reconcile(nil, nil)
	if !res.OK {
		t.Fatalf("OK = false on a clean tree with an empty register")
	}
}

func TestParseFailures(t *testing.T) {
	out := `=== RUN   TestSystemSeedReconcile
--- FAIL: TestSystemSeedReconcile (0.12s)
    --- FAIL: TestSystemSeedReconcile/drift (0.01s)
=== RUN   TestProjectDetailPanel_Chromedp
--- FAIL: TestProjectDetailPanel_Chromedp (4.30s)
--- PASS: TestHappy (0.00s)
ok   github.com/bobmcallan/satellites/internal/foo 0.4s
FAIL github.com/bobmcallan/satellites/tests/integration 5.1s`
	got := ParseFailures(out)
	want := []string{"TestSystemSeedReconcile", "TestProjectDetailPanel_Chromedp"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseFailures = %v; want %v", got, want)
	}
}

func TestParseFailures_None(t *testing.T) {
	out := `--- PASS: TestA (0.00s)
ok   github.com/bobmcallan/satellites/internal/foo 0.4s`
	if got := ParseFailures(out); len(got) != 0 {
		t.Fatalf("ParseFailures = %v; want empty", got)
	}
}

func TestParseRegister(t *testing.T) {
	body := "# Technical-debt register\n\nSome prose explaining the ratchet.\n\n" +
		"| check_id | story_id | reason |\n" +
		"| --- | --- | --- |\n" +
		"| TestSystemSeedReconcile | sty_real | system_seeds hash drift (real bug) |\n" +
		"| TestProjectDetailPanel_Chromedp | sty_flaky | chromedp headless flaky under WSL |\n"
	got := ParseRegister(body)
	want := []Entry{
		{CheckID: "TestSystemSeedReconcile", StoryID: "sty_real", Reason: "system_seeds hash drift (real bug)"},
		{CheckID: "TestProjectDetailPanel_Chromedp", StoryID: "sty_flaky", Reason: "chromedp headless flaky under WSL"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseRegister = %+v; want %+v", got, want)
	}
}

func TestParseRegister_EmptyOrProseOnly(t *testing.T) {
	if got := ParseRegister("# Register\n\nNo rows yet. The tree is clean.\n"); len(got) != 0 {
		t.Fatalf("ParseRegister = %+v; want empty", got)
	}
}

// An unowned row authored in the document round-trips as an Entry with an empty
// StoryID, which Reconcile then refuses.
func TestParseRegister_UnownedRow(t *testing.T) {
	body := "| check_id | story_id | reason |\n| --- | --- | --- |\n| TestSneaky |  | snuck in with no owner |\n"
	got := ParseRegister(body)
	if len(got) != 1 || got[0].CheckID != "TestSneaky" || got[0].StoryID != "" {
		t.Fatalf("ParseRegister = %+v; want one unowned TestSneaky row", got)
	}
}
