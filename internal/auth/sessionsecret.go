package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
)

const sessionSecretKey = "session_secret"

// LoadOrCreateSessionSecret returns the HMAC secret used for signed
// session cookies, persisted in the server_settings table.
//
// On first boot the table has no row; this function mints a fresh
// 32-byte secret, inserts it, and returns it. Subsequent boots return
// the same value — so login sessions survive container restarts and
// rolling deploys (the security is backed in the DB, not in
// per-process memory).
//
// Operators who want to manage the secret externally (fly.io secrets,
// a vault, etc.) should set SATELLITES_SESSION_SECRET — the caller in
// cmd/satellites-server prefers the env var when set.
func LoadOrCreateSessionSecret(ctx context.Context, db *sql.DB) ([]byte, error) {
	hexVal, err := readSessionSecret(ctx, db)
	if err == nil {
		return hex.DecodeString(hexVal)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("auth: load session secret: %w", err)
	}

	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("auth: rng: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
        INSERT INTO server_settings (key, value) VALUES ($1, $2)
        ON CONFLICT (key) DO NOTHING
    `, sessionSecretKey, hex.EncodeToString(secret)); err != nil {
		return nil, fmt.Errorf("auth: persist session secret: %w", err)
	}

	// Re-read — covers the race where two boots insert in parallel; the
	// ON CONFLICT clause means only one wins, and both should now read
	// the winning value.
	hexVal, err = readSessionSecret(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("auth: re-read session secret: %w", err)
	}
	return hex.DecodeString(hexVal)
}

func readSessionSecret(ctx context.Context, db *sql.DB) (string, error) {
	var v string
	err := db.QueryRowContext(ctx,
		`SELECT value FROM server_settings WHERE key = $1`,
		sessionSecretKey,
	).Scan(&v)
	return v, err
}
