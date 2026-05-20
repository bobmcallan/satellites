// Package auth provides V5's authentication primitives: users,
// api-keys, OAuth scaffolding, and the HTTP middleware that gates
// substrate verbs.
//
// The store is DB-backed against the users + api_keys tables (migration
// 0002). Api-keys are stored as SHA-256 hashes — high-entropy secrets
// don't need bcrypt and need to be O(1) lookupable by hash.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// Role enumerates the V5 user-role surface.
type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

// User is a satellites operator.
type User struct {
	ID          string
	Email       string
	DisplayName string
	Role        Role
	CreatedAt   time.Time
}

// APIKey is a credential authenticating HTTP/MCP requests.
type APIKey struct {
	ID        string
	UserID    string
	ProjectID string
	AgentName string
	KeyHash   string
	CreatedAt time.Time
	RevokedAt *time.Time
}

// ErrInvalidKey is returned by ValidateKey when the credential does
// not match any active row.
var ErrInvalidKey = errors.New("auth: invalid api key")

// Store wraps DB-backed auth operations.
type Store struct {
	DB *sql.DB
}

// New returns a Store bound to the given database/sql handle.
func New(db *sql.DB) *Store { return &Store{DB: db} }

// HashKey returns the deterministic hash used to store + look up api
// keys (SHA-256 hex).
func HashKey(rawKey string) string {
	h := sha256.Sum256([]byte(rawKey))
	return hex.EncodeToString(h[:])
}

func generateRawKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: rng: %w", err)
	}
	return "sk_" + hex.EncodeToString(b), nil
}

// CreateUser inserts a user idempotently on email; returns the user
// row (existing or new).
func (s *Store) CreateUser(ctx context.Context, id, email, displayName string, role Role) (*User, error) {
	if email == "" {
		return nil, fmt.Errorf("auth: email required")
	}
	if role == "" {
		role = RoleUser
	}
	if _, err := s.DB.ExecContext(ctx, `
        INSERT INTO users (id, email, display_name, role)
        VALUES ($1, $2, $3, $4)
        ON CONFLICT (email) DO NOTHING
    `, id, email, displayName, string(role)); err != nil {
		return nil, fmt.Errorf("auth: insert user: %w", err)
	}
	return s.GetUserByEmail(ctx, email)
}

// GetUserByEmail returns the user with the given email or sql.ErrNoRows.
func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	var u User
	if err := s.DB.QueryRowContext(ctx, `
        SELECT id, email, display_name, role, created_at
          FROM users WHERE email = $1
    `, email).Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.CreatedAt); err != nil {
		return nil, err
	}
	return &u, nil
}

// IssueAPIKey mints a fresh random api-key for (user, project, agent).
// Returns the raw key (only visible at issue time) and the row.
func (s *Store) IssueAPIKey(ctx context.Context, id, userID, projectID, agentName string) (string, *APIKey, error) {
	rawKey, err := generateRawKey()
	if err != nil {
		return "", nil, err
	}
	return s.IssueAPIKeyWithRaw(ctx, id, userID, projectID, agentName, rawKey)
}

// IssueAPIKeyWithRaw inserts an api-key with a caller-supplied raw
// value. Used by DevSeed to install predictable keys. Idempotent on
// the key_hash unique index — a re-insert with the same raw key
// returns the existing row.
func (s *Store) IssueAPIKeyWithRaw(ctx context.Context, id, userID, projectID, agentName, rawKey string) (string, *APIKey, error) {
	hash := HashKey(rawKey)

	var projectArg any
	if projectID != "" {
		projectArg = projectID
	}

	if _, err := s.DB.ExecContext(ctx, `
        INSERT INTO api_keys (id, user_id, project_id, agent_name, key_hash)
        VALUES ($1, $2, $3, $4, $5)
        ON CONFLICT (key_hash) DO NOTHING
    `, id, userID, projectArg, agentName, hash); err != nil {
		return "", nil, fmt.Errorf("auth: insert apikey: %w", err)
	}

	var k APIKey
	if err := s.DB.QueryRowContext(ctx, `
        SELECT id, user_id, COALESCE(project_id,''), agent_name, key_hash, created_at, revoked_at
          FROM api_keys WHERE key_hash = $1
    `, hash).Scan(&k.ID, &k.UserID, &k.ProjectID, &k.AgentName, &k.KeyHash, &k.CreatedAt, &k.RevokedAt); err != nil {
		return "", nil, err
	}
	return rawKey, &k, nil
}

// ValidateKey looks up the user for a given raw api-key. Returns
// ErrInvalidKey when the key is unknown or revoked.
func (s *Store) ValidateKey(ctx context.Context, rawKey string) (*User, error) {
	hash := HashKey(rawKey)
	var u User
	err := s.DB.QueryRowContext(ctx, `
        SELECT u.id, u.email, u.display_name, u.role, u.created_at
          FROM api_keys k
          JOIN users u ON u.id = k.user_id
         WHERE k.key_hash = $1 AND k.revoked_at IS NULL
    `, hash).Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidKey
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// RevokeKey sets revoked_at on the api-key matching the given id.
func (s *Store) RevokeKey(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx, `
        UPDATE api_keys SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL
    `, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("auth: apikey %s not found or already revoked", id)
	}
	return nil
}
