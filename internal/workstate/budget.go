// Delivered-context budget tracking (sty_e3e8f3ce, epic:qa-observability). The
// epic's backbone bet is that the agent-facing context "shrinks against a
// budget, never grows." That needs one tracked number and a baseline to ratchet
// against. This stores the always-on delivered-context size (the executor
// surface the spike baselined) as a time series on the same per-repo store, with
// exactly one row flagged as the baseline.
//
// Storage only — the sizing is order:2's assembleDeliveredContext and the
// ratchet comparison is a pure CLI helper; this package just persists the metric
// and answers "what is the baseline?".

package workstate

import (
	"database/sql"
	"fmt"
	"time"
)

// ContextSample is one recorded delivered-context measurement.
type ContextSample struct {
	Seq        int64
	TS         time.Time
	Bytes      int
	Tokens     int
	IsBaseline bool
	Components string // optional JSON breakdown, for the after-the-fact view
}

// RecordContextSample appends a measurement and returns its seq. When asBaseline
// is true the row becomes THE baseline: any prior baseline flag is cleared first
// (exactly one baseline at a time), atomically.
func (s *Store) RecordContextSample(sample ContextSample, asBaseline bool) (int64, error) {
	if sample.Bytes < 0 {
		return 0, fmt.Errorf("workstate: context sample bytes must be >= 0")
	}
	ts := sample.TS
	if ts.IsZero() {
		ts = time.Now()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if asBaseline {
		if _, err := tx.Exec(`UPDATE context_budget SET is_baseline = 0 WHERE is_baseline = 1`); err != nil {
			return 0, fmt.Errorf("workstate: clear baseline: %w", err)
		}
	}
	res, err := tx.Exec(
		`INSERT INTO context_budget (ts, bytes, tokens, is_baseline, components) VALUES (?, ?, ?, ?, ?)`,
		ts.UTC().Format(time.RFC3339), sample.Bytes, sample.Tokens, boolToInt(asBaseline), sample.Components,
	)
	if err != nil {
		return 0, fmt.Errorf("workstate: record context sample: %w", err)
	}
	seq, _ := res.LastInsertId()
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return seq, nil
}

// ContextBaseline returns the current baseline sample. ok is false when no
// baseline has been set yet.
func (s *Store) ContextBaseline() (ContextSample, bool, error) {
	row := s.db.QueryRow(
		`SELECT seq, ts, bytes, tokens, is_baseline, components
		   FROM context_budget WHERE is_baseline = 1 ORDER BY seq DESC LIMIT 1`,
	)
	cs, err := scanContextSample(row)
	if err == sql.ErrNoRows {
		return ContextSample{}, false, nil
	}
	if err != nil {
		return ContextSample{}, false, err
	}
	return cs, true, nil
}

// ContextSamples returns the most-recent samples, newest first (the tracked
// metric over time). limit <= 0 returns all.
func (s *Store) ContextSamples(limit int) ([]ContextSample, error) {
	q := `SELECT seq, ts, bytes, tokens, is_baseline, components FROM context_budget ORDER BY seq DESC`
	args := []any{}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ContextSample
	for rows.Next() {
		cs, err := scanContextSample(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, cs)
	}
	return out, rows.Err()
}

func scanContextSample(sc scanner) (ContextSample, error) {
	var cs ContextSample
	var ts string
	var isBaseline int
	if err := sc.Scan(&cs.Seq, &ts, &cs.Bytes, &cs.Tokens, &isBaseline, &cs.Components); err != nil {
		return ContextSample{}, err
	}
	cs.TS = parseRFC3339(ts)
	cs.IsBaseline = isBaseline != 0
	return cs, nil
}
