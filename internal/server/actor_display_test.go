package server

import (
	"testing"

	"github.com/bobmcallan/satellites/internal/processtrace"
)

// TestResolveActor pins the nil-safe wrapper: a nil resolver passes the id
// through (the unit-test path), a resolver maps known ids and leaves unknown
// ones as the raw id (sty_4a3a9ebf AC2).
func TestResolveActor(t *testing.T) {
	if got := resolveActor(nil, "usr_x"); got != "usr_x" {
		t.Fatalf("nil resolver should pass through, got %q", got)
	}
	r := func(id string) string {
		if id == "usr_known" {
			return "me@example.com"
		}
		return id
	}
	if got := resolveActor(r, "usr_known"); got != "me@example.com" {
		t.Errorf("known actor = %q, want resolved email", got)
	}
	if got := resolveActor(r, "usr_unknown"); got != "usr_unknown" {
		t.Errorf("unknown actor = %q, want raw id fallback", got)
	}
}

// TestMergedRowsResolvesActor: the LEDGER/LOG strip renders the resolved email
// for a known actor and the raw id for an unresolved one (sty_4a3a9ebf AC1/AC2).
func TestMergedRowsResolvesActor(t *testing.T) {
	entries := []processtrace.AnnotatedEntry{
		{LedgerEntry: processtrace.LedgerEntry{Kind: "review_accept", Actor: "usr_known"}},
		{LedgerEntry: processtrace.LedgerEntry{Kind: "comment", Actor: "usr_unknown"}},
	}
	r := func(id string) string {
		if id == "usr_known" {
			return "bobmcallan@gmail.com"
		}
		return id
	}
	rows := mergedRows(entries, r)
	seen := map[string]bool{}
	for _, row := range rows {
		seen[row.Actor] = true
	}
	if !seen["bobmcallan@gmail.com"] {
		t.Errorf("known actor not resolved to email; rows=%+v", rows)
	}
	if !seen["usr_unknown"] {
		t.Errorf("unknown actor should fall back to raw id; rows=%+v", rows)
	}
}
