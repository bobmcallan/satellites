package auth

import (
	"strings"
	"testing"
	"time"
)

func TestJWT_MintValidateRoundTrip(t *testing.T) {
	secret := []byte("test-secret-32-bytes-or-more-pad-pad-pad")
	now := time.Now().Unix()
	claims := &JWTClaims{
		Sub:      "usr_x",
		Email:    "x@example.com",
		Scope:    "satellites",
		ClientID: "cli_x",
		Iss:      "https://test/",
		Iat:      now,
		Exp:      now + 3600,
	}
	token, err := CreateJWT(claims, secret)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if strings.Count(token, ".") != 2 {
		t.Errorf("token shape: %q", token)
	}
	got, err := ValidateJWT(token, secret)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got.Sub != "usr_x" || got.Email != "x@example.com" {
		t.Errorf("claims roundtrip: %+v", got)
	}
}

func TestJWT_ExpiredRejected(t *testing.T) {
	secret := []byte("secret")
	claims := &JWTClaims{Sub: "u", Iat: 0, Exp: 1}
	token, _ := CreateJWT(claims, secret)
	if _, err := ValidateJWT(token, secret); err == nil {
		t.Error("expired token should not validate")
	}
}

func TestJWT_BadSignatureRejected(t *testing.T) {
	secret := []byte("secret")
	now := time.Now().Unix()
	claims := &JWTClaims{Sub: "u", Iat: now, Exp: now + 60}
	token, _ := CreateJWT(claims, secret)
	if _, err := ValidateJWT(token, []byte("wrong-secret")); err == nil {
		t.Error("bad-sig token should not validate")
	}
}

func TestLooksLikeJWT(t *testing.T) {
	cases := map[string]bool{
		"a.b.c":            true,
		"":                 false,
		"sk_abc":           false,
		"sk_a.b.c":         false,
		"only-two.parts":   false,
		"four.dots.in.tok": false,
	}
	for tok, want := range cases {
		if got := LooksLikeJWT(tok); got != want {
			t.Errorf("LooksLikeJWT(%q) = %v, want %v", tok, got, want)
		}
	}
}
