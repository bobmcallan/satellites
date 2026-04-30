package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestVerifyPKCE_Match(t *testing.T) {
	verifier := "this-is-a-test-verifier-with-enough-entropy-for-pkce"
	hash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])
	if !VerifyPKCE(verifier, challenge) {
		t.Fatalf("VerifyPKCE(verifier, challenge) = false, want true")
	}
}

func TestVerifyPKCE_Mismatch(t *testing.T) {
	if VerifyPKCE("verifier-a", "computed-from-something-else") {
		t.Fatalf("VerifyPKCE mismatched pair = true, want false")
	}
}

func TestVerifyPKCE_Empty(t *testing.T) {
	if VerifyPKCE("", "x") {
		t.Errorf("empty verifier accepted")
	}
	if VerifyPKCE("x", "") {
		t.Errorf("empty challenge accepted")
	}
}
