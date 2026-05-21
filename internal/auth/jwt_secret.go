package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
)

const jwtSecretKey = "oauth_jwt_secret"

// LoadOrCreateJWTSecret returns the HMAC secret used to sign OAuth
// access-token JWTs, persisted in the server_settings table. Same
// shape as LoadOrCreateSessionSecret — separate key so the two
// signing surfaces can be rotated independently.
func LoadOrCreateJWTSecret(ctx context.Context, db *sql.DB) ([]byte, error) {
	hexVal, err := readSetting(ctx, db, jwtSecretKey)
	if err == nil {
		return hex.DecodeString(hexVal)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("auth: load jwt secret: %w", err)
	}

	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("auth: rng: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
        INSERT INTO server_settings (key, value) VALUES ($1, $2)
        ON CONFLICT (key) DO NOTHING
    `, jwtSecretKey, hex.EncodeToString(secret)); err != nil {
		return nil, fmt.Errorf("auth: persist jwt secret: %w", err)
	}
	hexVal, err = readSetting(ctx, db, jwtSecretKey)
	if err != nil {
		return nil, fmt.Errorf("auth: re-read jwt secret: %w", err)
	}
	return hex.DecodeString(hexVal)
}

func readSetting(ctx context.Context, db *sql.DB, key string) (string, error) {
	var v string
	err := db.QueryRowContext(ctx,
		`SELECT value FROM server_settings WHERE key = $1`, key,
	).Scan(&v)
	return v, err
}
