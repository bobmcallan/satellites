package cli

import (
	"encoding/json"
	"testing"
)

// TestRequireParent: the operator override only applies to category:parent.
func TestRequireParent(t *testing.T) {
	if err := requireParent("parent", "sty_x"); err != nil {
		t.Errorf("parent should pass: %v", err)
	}
	if err := requireParent("fix", "sty_x"); err == nil {
		t.Errorf("a non-parent story must be refused")
	}
	if err := requireParent("", "sty_x"); err == nil {
		t.Errorf("an empty category must be refused")
	}
}

// TestSetStatusLedgerRequest: the override records a status_transition ledger row
// carrying to_status — the same projection path the gate uses.
func TestSetStatusLedgerRequest(t *testing.T) {
	req := setStatusLedgerRequest("sty_epic", "backlog")
	if req.StoryID != "sty_epic" || req.Kind != "status_transition" {
		t.Fatalf("req = %+v, want story=sty_epic kind=status_transition", req)
	}
	var p map[string]any
	if err := json.Unmarshal(req.Payload, &p); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if p["to_status"] != "backlog" {
		t.Errorf("to_status = %v, want backlog", p["to_status"])
	}
}
