package auth

import (
	"context"
	"errors"
	"time"
)

// ErrOAuthNotFound is the canonical not-found error returned by every
// OAuthStore Get* method.
var ErrOAuthNotFound = errors.New("oauth: not found")

// OAuthClient is a registered OAuth 2.1 client (RFC 7591).
type OAuthClient struct {
	ClientID                string   `json:"client_id"`
	ClientSecret            string   `json:"client_secret,omitempty"`
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	CreatedAt               int64    `json:"client_id_issued_at,omitempty"`
}

// OAuthSession is a pending /oauth/authorize request whose user has
// not yet completed the browser login. The mcp_session_id cookie
// carries the SessionID across the login round-trip;
// CompleteAuthorization consumes it.
type OAuthSession struct {
	SessionID     string
	ClientID      string
	RedirectURI   string
	State         string
	CodeChallenge string
	CodeMethod    string
	Scope         string
	CreatedAt     time.Time
}

// OAuthCode is a single-use authorization code minted at the end of
// the browser flow. Exchanged at /oauth/token for an access+refresh
// token pair.
type OAuthCode struct {
	Code          string
	ClientID      string
	UserID        string
	RedirectURI   string
	CodeChallenge string
	Scope         string
	ExpiresAt     time.Time
	Used          bool
}

// OAuthRefreshToken is the long-lived refresh credential. Rotated on
// every successful refresh grant — old token deleted, new one issued.
type OAuthRefreshToken struct {
	Token     string
	UserID    string
	ClientID  string
	Scope     string
	ExpiresAt time.Time
}

// OAuthStore persists OAuth clients, in-flight auth sessions,
// single-use codes, and refresh tokens. The Postgres-backed
// implementation lives in oauth_store_postgres.go.
type OAuthStore interface {
	SaveClient(ctx context.Context, c *OAuthClient) error
	GetClient(ctx context.Context, clientID string) (*OAuthClient, error)

	SaveSession(ctx context.Context, s *OAuthSession) error
	GetSession(ctx context.Context, sessionID string) (*OAuthSession, error)
	GetSessionByClientID(ctx context.Context, clientID string) (*OAuthSession, error)
	DeleteSession(ctx context.Context, sessionID string) error

	SaveCode(ctx context.Context, c *OAuthCode) error
	GetCode(ctx context.Context, code string) (*OAuthCode, error)
	MarkCodeUsed(ctx context.Context, code string) error

	SaveRefreshToken(ctx context.Context, t *OAuthRefreshToken) error
	GetRefreshToken(ctx context.Context, token string) (*OAuthRefreshToken, error)
	DeleteRefreshToken(ctx context.Context, token string) error
}
