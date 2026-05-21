package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// OAuthStorePG is a Postgres-backed implementation of OAuthStore.
type OAuthStorePG struct {
	DB *sql.DB
}

// NewOAuthStore returns an OAuthStorePG bound to db.
func NewOAuthStore(db *sql.DB) *OAuthStorePG { return &OAuthStorePG{DB: db} }

func (s *OAuthStorePG) SaveClient(ctx context.Context, c *OAuthClient) error {
	_, err := s.DB.ExecContext(ctx, `
        INSERT INTO oauth_clients
            (client_id, client_secret, client_name, redirect_uris,
             grant_types, response_types, token_endpoint_auth_method, created_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
        ON CONFLICT (client_id) DO UPDATE SET
            client_secret = EXCLUDED.client_secret,
            client_name = EXCLUDED.client_name,
            redirect_uris = EXCLUDED.redirect_uris,
            grant_types = EXCLUDED.grant_types,
            response_types = EXCLUDED.response_types,
            token_endpoint_auth_method = EXCLUDED.token_endpoint_auth_method
    `, c.ClientID, c.ClientSecret, c.ClientName,
		pq.Array(c.RedirectURIs), pq.Array(c.GrantTypes), pq.Array(c.ResponseTypes),
		c.TokenEndpointAuthMethod, c.CreatedAt)
	if err != nil {
		return fmt.Errorf("oauth: save client: %w", err)
	}
	return nil
}

func (s *OAuthStorePG) GetClient(ctx context.Context, clientID string) (*OAuthClient, error) {
	row := s.DB.QueryRowContext(ctx, `
        SELECT client_id, client_secret, client_name, redirect_uris,
               grant_types, response_types, token_endpoint_auth_method, created_at
        FROM oauth_clients WHERE client_id = $1
    `, clientID)
	var (
		c  OAuthClient
		ru pq.StringArray
		gt pq.StringArray
		rt pq.StringArray
	)
	if err := row.Scan(&c.ClientID, &c.ClientSecret, &c.ClientName, &ru, &gt, &rt,
		&c.TokenEndpointAuthMethod, &c.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrOAuthNotFound
		}
		return nil, fmt.Errorf("oauth: get client: %w", err)
	}
	c.RedirectURIs = []string(ru)
	c.GrantTypes = []string(gt)
	c.ResponseTypes = []string(rt)
	return &c, nil
}

func (s *OAuthStorePG) SaveSession(ctx context.Context, sess *OAuthSession) error {
	_, err := s.DB.ExecContext(ctx, `
        INSERT INTO oauth_sessions
            (session_id, client_id, redirect_uri, state, code_challenge, code_method, scope, created_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
    `, sess.SessionID, sess.ClientID, sess.RedirectURI, sess.State,
		sess.CodeChallenge, sess.CodeMethod, sess.Scope, sess.CreatedAt.UTC())
	if err != nil {
		return fmt.Errorf("oauth: save session: %w", err)
	}
	return nil
}

func (s *OAuthStorePG) GetSession(ctx context.Context, sessionID string) (*OAuthSession, error) {
	row := s.DB.QueryRowContext(ctx, `
        SELECT session_id, client_id, redirect_uri, state, code_challenge, code_method, scope, created_at
        FROM oauth_sessions WHERE session_id = $1
    `, sessionID)
	return scanSession(row)
}

func (s *OAuthStorePG) GetSessionByClientID(ctx context.Context, clientID string) (*OAuthSession, error) {
	row := s.DB.QueryRowContext(ctx, `
        SELECT session_id, client_id, redirect_uri, state, code_challenge, code_method, scope, created_at
        FROM oauth_sessions WHERE client_id = $1
        ORDER BY created_at DESC LIMIT 1
    `, clientID)
	return scanSession(row)
}

func (s *OAuthStorePG) DeleteSession(ctx context.Context, sessionID string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM oauth_sessions WHERE session_id = $1`, sessionID)
	if err != nil {
		return fmt.Errorf("oauth: delete session: %w", err)
	}
	return nil
}

func (s *OAuthStorePG) SaveCode(ctx context.Context, c *OAuthCode) error {
	_, err := s.DB.ExecContext(ctx, `
        INSERT INTO oauth_codes
            (code, client_id, user_id, redirect_uri, code_challenge, scope, expires_at, used)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
    `, c.Code, c.ClientID, c.UserID, c.RedirectURI, c.CodeChallenge, c.Scope, c.ExpiresAt.UTC(), c.Used)
	if err != nil {
		return fmt.Errorf("oauth: save code: %w", err)
	}
	return nil
}

func (s *OAuthStorePG) GetCode(ctx context.Context, code string) (*OAuthCode, error) {
	row := s.DB.QueryRowContext(ctx, `
        SELECT code, client_id, user_id, redirect_uri, code_challenge, scope, expires_at, used
        FROM oauth_codes WHERE code = $1
    `, code)
	var c OAuthCode
	if err := row.Scan(&c.Code, &c.ClientID, &c.UserID, &c.RedirectURI,
		&c.CodeChallenge, &c.Scope, &c.ExpiresAt, &c.Used); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrOAuthNotFound
		}
		return nil, fmt.Errorf("oauth: get code: %w", err)
	}
	if c.Used {
		return nil, ErrOAuthNotFound
	}
	if c.ExpiresAt.Before(time.Now()) {
		return nil, ErrOAuthNotFound
	}
	return &c, nil
}

func (s *OAuthStorePG) MarkCodeUsed(ctx context.Context, code string) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE oauth_codes SET used = TRUE WHERE code = $1 AND used = FALSE`, code)
	if err != nil {
		return fmt.Errorf("oauth: mark code used: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrOAuthNotFound
	}
	return nil
}

func (s *OAuthStorePG) SaveRefreshToken(ctx context.Context, t *OAuthRefreshToken) error {
	_, err := s.DB.ExecContext(ctx, `
        INSERT INTO oauth_refresh_tokens (token, user_id, client_id, scope, expires_at)
        VALUES ($1, $2, $3, $4, $5)
    `, t.Token, t.UserID, t.ClientID, t.Scope, t.ExpiresAt.UTC())
	if err != nil {
		return fmt.Errorf("oauth: save refresh token: %w", err)
	}
	return nil
}

func (s *OAuthStorePG) GetRefreshToken(ctx context.Context, token string) (*OAuthRefreshToken, error) {
	row := s.DB.QueryRowContext(ctx, `
        SELECT token, user_id, client_id, scope, expires_at
        FROM oauth_refresh_tokens WHERE token = $1
    `, token)
	var t OAuthRefreshToken
	if err := row.Scan(&t.Token, &t.UserID, &t.ClientID, &t.Scope, &t.ExpiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrOAuthNotFound
		}
		return nil, fmt.Errorf("oauth: get refresh token: %w", err)
	}
	if t.ExpiresAt.Before(time.Now()) {
		return nil, ErrOAuthNotFound
	}
	return &t, nil
}

func (s *OAuthStorePG) DeleteRefreshToken(ctx context.Context, token string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM oauth_refresh_tokens WHERE token = $1`, token)
	if err != nil {
		return fmt.Errorf("oauth: delete refresh token: %w", err)
	}
	return nil
}

func scanSession(row *sql.Row) (*OAuthSession, error) {
	var sess OAuthSession
	if err := row.Scan(&sess.SessionID, &sess.ClientID, &sess.RedirectURI,
		&sess.State, &sess.CodeChallenge, &sess.CodeMethod, &sess.Scope, &sess.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrOAuthNotFound
		}
		return nil, fmt.Errorf("oauth: scan session: %w", err)
	}
	return &sess, nil
}
