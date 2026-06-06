package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// stubDispatch returns canned responses keyed by verb name, so the audit
// transport is exercised without a server.
func stubDispatch(responses map[string]string) verbDispatch {
	return func(_ context.Context, name string, _ json.RawMessage) (json.RawMessage, error) {
		if r, ok := responses[name]; ok {
			return json.RawMessage(r), nil
		}
		return json.RawMessage(`{}`), nil
	}
}

// TestEvidenceAudit_SingleStory_RendersFindings: an in_progress story whose
// ledger shows a deploy success is flagged ungated-deploy via the transport.
func TestEvidenceAudit_SingleStory_RendersFindings(t *testing.T) {
	disp := stubDispatch(map[string]string{
		"document_get": `{"document":{"id":"sty_x","type":"story","status":"in_progress"}}`,
		"ledger_list":  `{"entries":[{"kind":"ci_result","body":"ci deploy: success","payload":{"stage":"deploy","result":"success","ref":"v9"},"created_at":"2026-06-06T00:00:01Z"}]}`,
	})
	var buf bytes.Buffer
	if err := runEvidenceAudit(context.Background(), &buf, evidenceAuditOpts{Story: "sty_x"}, disp); err != nil {
		t.Fatalf("audit: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "ungated-deploy") || !strings.Contains(out, "sty_x") {
		t.Fatalf("expected an ungated-deploy finding for sty_x:\n%s", out)
	}
}

// TestEvidenceAudit_CleanStory_NoFindings: a well-formed story renders the clean line.
func TestEvidenceAudit_CleanStory_NoFindings(t *testing.T) {
	disp := stubDispatch(map[string]string{
		"document_get": `{"document":{"id":"sty_ok","type":"story","status":"done"}}`,
		"ledger_list": `{"entries":[
			{"kind":"review_requested","payload":{"from_status":"in_progress"},"created_at":"2026-06-06T00:00:01Z"},
			{"kind":"review_accept","body":"ok","payload":{"from_status":"in_progress","gate":"satellites-story-done-review"},"created_at":"2026-06-06T00:00:02Z"},
			{"kind":"status_transition","payload":{"from_status":"in_progress","to_status":"done"},"created_at":"2026-06-06T00:00:03Z"}
		]}`,
	})
	var buf bytes.Buffer
	if err := runEvidenceAudit(context.Background(), &buf, evidenceAuditOpts{Story: "sty_ok"}, disp); err != nil {
		t.Fatalf("audit: %v", err)
	}
	if !strings.Contains(buf.String(), "clean") {
		t.Fatalf("expected a clean verdict:\n%s", buf.String())
	}
}

// TestEvidenceAudit_JSON: --json emits a findings array addressable by story.
func TestEvidenceAudit_JSON(t *testing.T) {
	disp := stubDispatch(map[string]string{
		"document_get": `{"document":{"id":"sty_j","type":"story","status":"in_progress"}}`,
		"ledger_list":  `{"entries":[{"kind":"status_transition","payload":{"from_status":"backlog","to_status":"done"},"created_at":"2026-06-06T00:00:01Z"}]}`,
	})
	var buf bytes.Buffer
	if err := runEvidenceAudit(context.Background(), &buf, evidenceAuditOpts{Story: "sty_j", JSON: true}, disp); err != nil {
		t.Fatalf("audit json: %v", err)
	}
	var findings []struct {
		StoryID string `json:"story_id"`
		Anomaly string `json:"anomaly"`
	}
	if err := json.Unmarshal(buf.Bytes(), &findings); err != nil {
		t.Fatalf("decode findings: %v\n%s", err, buf.String())
	}
	if len(findings) == 0 || findings[0].StoryID != "sty_j" {
		t.Fatalf("expected findings addressable by sty_j, got %+v", findings)
	}
}

// TestEvidenceAudit_Sweep: with no story arg it lists stories then audits each.
func TestEvidenceAudit_Sweep(t *testing.T) {
	disp := func(_ context.Context, name string, _ json.RawMessage) (json.RawMessage, error) {
		switch name {
		case "document_list":
			return json.RawMessage(`{"items":[{"id":"sty_1"}]}`), nil
		case "document_get":
			return json.RawMessage(`{"document":{"id":"sty_1","type":"story","status":"in_progress"}}`), nil
		case "ledger_list":
			return json.RawMessage(`{"entries":[{"kind":"step_summary","body":"x","payload":{"from_status":"in_progress"},"created_at":"2026-06-06T00:00:01Z"}]}`), nil
		}
		return json.RawMessage(`{}`), nil
	}
	var buf bytes.Buffer
	if err := runEvidenceAudit(context.Background(), &buf, evidenceAuditOpts{}, disp); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	// sty_1 is in_progress with no review_requested ⇒ unengaged-work.
	if !strings.Contains(buf.String(), "unengaged-work") {
		t.Fatalf("expected unengaged-work in sweep:\n%s", buf.String())
	}
}
