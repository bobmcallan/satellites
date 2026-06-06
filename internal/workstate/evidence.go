// Durable QA-evidence capture (sty_7d2e9847, epic:qa-observability). The spike
// (sty_ff49fec1) found gates run in-place with no logging: the gate skill +
// decision + reject reasons print to stdout and are lost, and CI outcomes never
// link to a story. This lands the capture on the same per-repo store order:6
// consolidated onto — one durable, queryable QA trail per story, read out of
// band by the background audit (order:9) and the QA view.
//
// A single qa_evidence table with a `kind` discriminator (gate | ci) rather than
// two: a story's QA trail is one stream. Unlike the reviewer-loop inbox/claim
// (transient, dropped by CleanupWork on terminal flush), evidence is the durable
// record — it is NOT cleaned up, so a completed story's QA trail survives for the
// audit and the after-the-fact view.

package workstate

import (
	"fmt"
	"strings"
	"time"
)

// Evidence kinds — the QA-trail discriminator.
const (
	EvidenceGate = "gate" // a reviewer gate run: label=gate skill, decision=accept/reject
	EvidenceCI   = "ci"   // a CI stage outcome: label=stage, decision=success/failure
)

// Evidence is one durable QA-trail row for a story. Label/Decision are kind-
// specific (gate skill + accept/reject, or CI stage + success/failure); Notes
// carries reject reasons or a CI detail line; Ref carries a commit sha or run
// URL for CI. Seq/TS are store-assigned.
type Evidence struct {
	Seq        int64
	Story      string
	Kind       string
	Label      string
	Decision   string
	Notes      string
	FromStatus string
	ToStatus   string
	Ref        string
	TS         time.Time
}

// RecordEvidence appends one durable QA-evidence row and returns its seq.
func (s *Store) RecordEvidence(e Evidence) (int64, error) {
	if strings.TrimSpace(e.Story) == "" || strings.TrimSpace(e.Kind) == "" {
		return 0, fmt.Errorf("workstate: evidence needs story and kind")
	}
	ts := e.TS
	if ts.IsZero() {
		ts = time.Now()
	}
	res, err := s.db.Exec(
		`INSERT INTO qa_evidence (story, kind, label, decision, notes, from_status, to_status, ref, ts)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Story, e.Kind, e.Label, e.Decision, e.Notes, e.FromStatus, e.ToStatus, e.Ref,
		ts.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return 0, fmt.Errorf("workstate: record evidence: %w", err)
	}
	return res.LastInsertId()
}

// ListEvidence returns a story's QA-evidence rows, oldest seq first — the
// out-of-band read for the audit and the QA view.
func (s *Store) ListEvidence(story string) ([]Evidence, error) {
	rows, err := s.db.Query(
		`SELECT seq, story, kind, label, decision, notes, from_status, to_status, ref, ts
		   FROM qa_evidence WHERE story = ? ORDER BY seq ASC`,
		story,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Evidence
	for rows.Next() {
		var e Evidence
		var ts string
		if err := rows.Scan(&e.Seq, &e.Story, &e.Kind, &e.Label, &e.Decision, &e.Notes,
			&e.FromStatus, &e.ToStatus, &e.Ref, &ts); err != nil {
			return nil, err
		}
		e.TS = parseRFC3339(ts)
		out = append(out, e)
	}
	return out, rows.Err()
}
