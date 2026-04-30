package auth

import (
	"strings"
	"testing"
	"time"
)

func TestJWT_RoundTrip(t *testing.T) {
	secret := []byte("test-secret-32-bytes-or-more-for-hmac")
	in := &JWTClaims{
		Sub:   "user_alice",
		Email: "alice@example.com",
		Iss:   "satellites",
		Iat:   time.Now().Unix(),
		Exp:   time.Now().Add(1 * time.Hour).Unix(),
	}
	token, err := CreateJWT(in, secret)
	if err != nil {
		t.Fatalf("CreateJWT: %v", err)
	}
	if !LooksLikeJWT(token) {
		t.Fatalf("LooksLikeJWT(%q) = false, want true", token)
	}
	out, err := ValidateJWT(token, secret)
	if err != nil {
		t.Fatalf("ValidateJWT: %v", err)
	}
	if out.Sub != in.Sub || out.Email != in.Email {
		t.Errorf("roundtrip claims mismatch: in=%+v out=%+v", in, out)
	}
}

func TestJWT_BadSignature(t *testing.T) {
	secret := []byte("right-secret")
	wrong := []byte("wrong-secret")
	token, err := CreateJWT(&JWTClaims{Sub: "x", Iss: "satellites", Iat: time.Now().Unix(), Exp: time.Now().Add(time.Minute).Unix()}, secret)
	if err != nil {
		t.Fatalf("CreateJWT: %v", err)
	}
	if _, err := ValidateJWT(token, wrong); err == nil {
		t.Fatalf("ValidateJWT(wrong secret) = nil, want error")
	}
}

func TestJWT_Expired(t *testing.T) {
	secret := []byte("s")
	token, _ := CreateJWT(&JWTClaims{Sub: "x", Iss: "satellites", Iat: time.Now().Add(-2 * time.Hour).Unix(), Exp: time.Now().Add(-1 * time.Hour).Unix()}, secret)
	_, err := ValidateJWT(token, secret)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("ValidateJWT(expired) err=%v, want 'expired'", err)
	}
}

func TestJWT_MissingExp(t *testing.T) {
	secret := []byte("s")
	token, _ := CreateJWT(&JWTClaims{Sub: "x", Iss: "satellites", Iat: time.Now().Unix()}, secret)
	if _, err := ValidateJWT(token, secret); err == nil {
		t.Fatalf("ValidateJWT(no-exp) = nil, want error")
	}
}

func TestJWT_MalformedShape(t *testing.T) {
	for _, bad := range []string{"", "not-a-jwt", "only.two", "way.too.many.parts.here"} {
		if _, err := ValidateJWT(bad, []byte("s")); err == nil {
			t.Errorf("ValidateJWT(%q) = nil, want error", bad)
		}
	}
}

func TestLooksLikeJWT(t *testing.T) {
	cases := []struct {
		token string
		want  bool
	}{
		{"a.b.c", true},
		{"", false},
		{"sat_abc", false},
		{"a.b", false},
		{"a.b.c.d", false},
		{"no-dots", false},
	}
	for _, tc := range cases {
		if got := LooksLikeJWT(tc.token); got != tc.want {
			t.Errorf("LooksLikeJWT(%q) = %v, want %v", tc.token, got, tc.want)
		}
	}
}
