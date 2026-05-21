package auth

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// ErrInvalidPassword is returned by VerifyPassword on a bad credential.
var ErrInvalidPassword = errors.New("auth: invalid password")

// SetPassword hashes raw with bcrypt and writes it into the user row.
// Idempotent in the sense of repeatable; each call writes a fresh hash.
func (s *Store) SetPassword(ctx context.Context, userID, raw string) error {
	if raw == "" {
		return fmt.Errorf("auth: empty password")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(raw), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("auth: bcrypt: %w", err)
	}
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE users SET password_hash = $1 WHERE id = $2`,
		string(h), userID); err != nil {
		return fmt.Errorf("auth: set password: %w", err)
	}
	return nil
}

// VerifyPassword resolves a (email, raw-password) pair to the user row.
// Returns ErrInvalidPassword when the email is unknown OR the password
// does not match — same error in both cases so the response cannot leak
// account-existence to callers.
func (s *Store) VerifyPassword(ctx context.Context, email, raw string) (*User, error) {
	var (
		u    User
		hash string
	)
	err := s.DB.QueryRowContext(ctx, `
        SELECT id, email, display_name, role, created_at, password_hash
          FROM users WHERE email = $1
    `, email).Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.CreatedAt, &hash)
	if err != nil {
		return nil, ErrInvalidPassword
	}
	if hash == "" {
		return nil, ErrInvalidPassword
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(raw)); err != nil {
		return nil, ErrInvalidPassword
	}
	return &u, nil
}
