package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestVerifyPKCE_RoundTrip(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	hash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])

	if !VerifyPKCE(verifier, challenge) {
		t.Error("matching verifier+challenge should verify")
	}
	if VerifyPKCE("wrong", challenge) {
		t.Error("wrong verifier should not verify")
	}
	if VerifyPKCE(verifier, "wrong") {
		t.Error("wrong challenge should not verify")
	}
	if VerifyPKCE("", challenge) {
		t.Error("empty verifier should not verify")
	}
	if VerifyPKCE(verifier, "") {
		t.Error("empty challenge should not verify")
	}
}
