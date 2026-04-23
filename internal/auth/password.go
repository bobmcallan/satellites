package auth

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// BcryptCost is the bcrypt work factor used by HashPassword. AC 5 requires
// cost ≥ 10; 12 is the current best-practice default.
const BcryptCost = 12

// ErrPasswordMismatch is returned by VerifyPassword when hash and plain
// don't match. Callers should treat this identically to "no such user" to
// avoid leaking account existence.
var ErrPasswordMismatch = errors.New("auth: password mismatch")

// HashPassword derives a bcrypt hash from the plaintext password at
// BcryptCost. Never log the plain input.
func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), BcryptCost)
	if err != nil {
		return "", fmt.Errorf("auth: hash password: %w", err)
	}
	return string(b), nil
}

// VerifyPassword returns nil when plain matches the stored hash.
// ErrPasswordMismatch on mismatch; wrapped bcrypt error otherwise.
func VerifyPassword(hash, plain string) error {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain))
	if err == nil {
		return nil
	}
	if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		return ErrPasswordMismatch
	}
	return fmt.Errorf("auth: verify password: %w", err)
}
