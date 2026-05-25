package document

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// SystemSeed is a row in the system_seeds registry. Identity is (name);
// EmbeddedHash is the sha256 of Body bytes recorded at the last apply.
type SystemSeed struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Body         string    `json:"body"`
	EmbeddedHash string    `json:"embedded_hash"`
	AppliedAt    time.Time `json:"applied_at"`
}

// SystemSeedStore wraps the system_seeds table. There are no verb-layer
// writes — only the boot reconciler calls Upsert.
type SystemSeedStore struct {
	DB *sql.DB
}

// NewSystemSeedStore returns a store bound to the given handle.
func NewSystemSeedStore(db *sql.DB) *SystemSeedStore { return &SystemSeedStore{DB: db} }

// Get returns the system_seeds row for name, or ErrNotFound.
func (s *SystemSeedStore) Get(ctx context.Context, name string) (SystemSeed, error) {
	row := s.DB.QueryRowContext(ctx, `
        SELECT id, name, body, embedded_hash, applied_at
        FROM system_seeds
        WHERE name = $1
    `, name)
	var out SystemSeed
	if err := row.Scan(&out.ID, &out.Name, &out.Body, &out.EmbeddedHash, &out.AppliedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SystemSeed{}, ErrNotFound
		}
		return SystemSeed{}, fmt.Errorf("system_seed: scan: %w", err)
	}
	return out, nil
}

// List returns every row, ordered by name. Useful for diagnostics.
func (s *SystemSeedStore) List(ctx context.Context) ([]SystemSeed, error) {
	rows, err := s.DB.QueryContext(ctx, `
        SELECT id, name, body, embedded_hash, applied_at
        FROM system_seeds
        ORDER BY name ASC
    `)
	if err != nil {
		return nil, fmt.Errorf("system_seed: list: %w", err)
	}
	defer rows.Close()
	var out []SystemSeed
	for rows.Next() {
		var r SystemSeed
		if err := rows.Scan(&r.ID, &r.Name, &r.Body, &r.EmbeddedHash, &r.AppliedAt); err != nil {
			return nil, fmt.Errorf("system_seed: list scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// upsert inserts a new row or replaces body+hash+applied_at on an
// existing row. Unexported — the boot reconciler is the only legitimate
// caller, reached via ReconcileSystemSeed.
func (s *SystemSeedStore) upsert(ctx context.Context, name, body, hash string, now time.Time) (SystemSeed, error) {
	now = now.UTC()
	id := fmt.Sprintf("sysd_%s", uuid.NewString()[:8])
	row := s.DB.QueryRowContext(ctx, `
        INSERT INTO system_seeds (id, name, body, embedded_hash, applied_at)
        VALUES ($1, $2, $3, $4, $5)
        ON CONFLICT (name) DO UPDATE
            SET body          = EXCLUDED.body,
                embedded_hash = EXCLUDED.embedded_hash,
                applied_at    = EXCLUDED.applied_at
        RETURNING id, name, body, embedded_hash, applied_at
    `, id, name, body, hash, now)
	var out SystemSeed
	if err := row.Scan(&out.ID, &out.Name, &out.Body, &out.EmbeddedHash, &out.AppliedAt); err != nil {
		return SystemSeed{}, fmt.Errorf("system_seed: upsert: %w", err)
	}
	return out, nil
}

// HashBody returns the canonical sha256-hex of the seed body bytes.
// Hex is used (not raw bytes) so the column stays human-comparable in
// psql diagnostics.
func HashBody(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// ReconcileResult reports what changed during one reconciliation pass.
type ReconcileResult struct {
	Name    string // seed name
	Changed bool   // true when system_seeds was inserted or its hash flipped
	Created bool   // true when this was the first apply for the name
}

// ReconcileSystemSeed brings (system_seeds[name], documents[system/name])
// into agreement with the supplied body. Idempotent: when the stored
// embedded_hash matches the body's hash, zero writes occur and Changed
// is false.
//
// Boot calls this once per embedded artifact. A binary release that
// changes one artifact rewrites exactly that row + appends one
// document_versions row; unchanged artifacts trigger no writes.
func ReconcileSystemSeed(ctx context.Context, sys *SystemSeedStore, docs *Store, name, body, createdBy string, now time.Time) (ReconcileResult, error) {
	hash := HashBody(body)
	existing, err := sys.Get(ctx, name)
	switch {
	case err == nil && existing.EmbeddedHash == hash:
		return ReconcileResult{Name: name, Changed: false, Created: false}, nil
	case err != nil && !errors.Is(err, ErrNotFound):
		return ReconcileResult{}, err
	}
	created := errors.Is(err, ErrNotFound)
	if _, err := sys.upsert(ctx, name, body, hash, now); err != nil {
		return ReconcileResult{}, err
	}
	if err := SeedSystem(ctx, docs, name, body, createdBy, now); err != nil {
		return ReconcileResult{}, fmt.Errorf("system_seed: mirror to documents: %w", err)
	}
	return ReconcileResult{Name: name, Changed: true, Created: created}, nil
}
