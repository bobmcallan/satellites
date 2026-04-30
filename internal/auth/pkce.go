package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
)

// VerifyPKCE checks an RFC 7636 S256 code verifier against its challenge.
// The challenge is base64url(sha256(verifier)) without padding. Constant
// time comparison guards against timing oracles on the verifier.
func VerifyPKCE(verifier, challenge string) bool {
	if verifier == "" || challenge == "" {
		return false
	}
	hash := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(hash[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}
