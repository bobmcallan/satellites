package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// JWTClaims is the satellites-issued OAuth access token payload. Standard
// claims (iss/sub/iat/exp) plus optional identity hints (email/name) and
// the OAuth-specific scope/client_id. Hand-rolled because HS256 with a
// stable schema doesn't justify a JWT library.
type JWTClaims struct {
	Sub      string `json:"sub"`
	Email    string `json:"email,omitempty"`
	Name     string `json:"name,omitempty"`
	Provider string `json:"provider,omitempty"`
	Scope    string `json:"scope,omitempty"`
	ClientID string `json:"client_id,omitempty"`
	Iss      string `json:"iss"`
	Iat      int64  `json:"iat"`
	Exp      int64  `json:"exp"`
}

// CreateJWT mints an HS256 JWT. Returns "header.payload.signature".
// Caller is responsible for setting Iat and Exp on claims.
func CreateJWT(claims *JWTClaims, secret []byte) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payloadBytes, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal jwt claims: %w", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	sigInput := header + "." + payload
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(sigInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return sigInput + "." + sig, nil
}

// ValidateJWT parses and verifies a JWT. When secret is non-empty the
// HMAC-SHA256 signature is checked; an empty secret skips signature
// verification (intended for tests, never production paths). exp is always
// enforced — a missing or past exp is an error.
func ValidateJWT(token string, secret []byte) (*JWTClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid jwt format: expected 3 parts, got %d", len(parts))
	}
	if len(secret) > 0 {
		sigInput := parts[0] + "." + parts[1]
		mac := hmac.New(sha256.New, secret)
		mac.Write([]byte(sigInput))
		expectedSig := mac.Sum(nil)
		actualSig, err := base64.RawURLEncoding.DecodeString(parts[2])
		if err != nil {
			return nil, fmt.Errorf("invalid jwt signature encoding: %w", err)
		}
		if !hmac.Equal(expectedSig, actualSig) {
			return nil, fmt.Errorf("invalid jwt signature")
		}
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid jwt payload encoding: %w", err)
	}
	var claims JWTClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, fmt.Errorf("invalid jwt payload json: %w", err)
	}
	if claims.Exp == 0 {
		return nil, fmt.Errorf("jwt missing exp claim")
	}
	if claims.Exp < time.Now().Unix() {
		return nil, fmt.Errorf("jwt expired")
	}
	return &claims, nil
}

// LooksLikeJWT cheaply checks whether token has the three-part dot shape
// without parsing. Used by BearerValidator to decide whether to attempt
// JWT validation before falling through to provider userinfo lookups.
func LooksLikeJWT(token string) bool {
	if token == "" {
		return false
	}
	if strings.HasPrefix(token, satelliteBearerPrefix) {
		return false
	}
	dots := 0
	for i := 0; i < len(token); i++ {
		if token[i] == '.' {
			dots++
			if dots > 2 {
				return false
			}
		}
	}
	return dots == 2
}
