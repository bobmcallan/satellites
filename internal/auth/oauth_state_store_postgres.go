package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

// PGStateStore is the durable implementation of StateStore. State rows
// outlive the process that minted them, so a callback that lands on a
// different instance — or on the same instance after a restart — can
// still validate.
//
// Schema lives in migration 0016_oauth_states.up.sql.
type PGStateStore struct {
	DB  *sql.DB
	TTL time.Duration
}

// NewPGStateStore returns a PG-backed StateStore. ttl <= 0 picks the
// 10-minute default that matches the in-memory implementation.
func NewPGStateStore(db *sql.DB, ttl time.Duration) *PGStateStore {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &PGStateStore{DB: db, TTL: ttl}
}

// Mint inserts a fresh random state row and returns the token. The row
// is pruned on its first successful Consume; expired-but-not-consumed
// rows are swept lazily inside Consume.
func (s *PGStateStore) Mint() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("oauth: rng: %w", err)
	}
	id := base64.RawURLEncoding.EncodeToString(buf)
	if _, err := s.DB.Exec(
		`INSERT INTO oauth_states (state, expires_at) VALUES ($1, $2)`,
		id, time.Now().Add(s.TTL).UTC(),
	); err != nil {
		return "", fmt.Errorf("oauth: insert state: %w", err)
	}
	return id, nil
}

// Consume deletes the row atomically; replay fails because the second
// DELETE matches no rows. Expired rows are deleted too (and reported as
// "expired state") so the table doesn't grow without bound.
func (s *PGStateStore) Consume(id string) error {
	if id == "" {
		return errors.New("oauth: empty state")
	}
	var expiresAt time.Time
	err := s.DB.QueryRow(
		`DELETE FROM oauth_states WHERE state = $1 RETURNING expires_at`,
		id,
	).Scan(&expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("oauth: unknown state")
	}
	if err != nil {
		return fmt.Errorf("oauth: delete state: %w", err)
	}
	if time.Now().After(expiresAt) {
		return errors.New("oauth: expired state")
	}
	return nil
}
