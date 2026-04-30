package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/surrealdb/surrealdb.go"
	surrealmodels "github.com/surrealdb/surrealdb.go/pkg/models"
	"github.com/ternarybob/arbor"
)

// SurrealOAuthStore is the SurrealDB-backed OAuthStore. Tables are
// created idempotently in NewSurrealOAuthStore via DEFINE TABLE IF NOT
// EXISTS. Mirrors the pattern used by SurrealSessionStore.
type SurrealOAuthStore struct {
	db     *surrealdb.DB
	logger arbor.ILogger
}

// ErrOAuthNotFound is returned when a client/session/code/refresh-token
// lookup misses. Distinguishes "not in store" from transport/SQL errors.
var ErrOAuthNotFound = errors.New("auth: oauth row not found")

// NewSurrealOAuthStore wraps db and ensures the four oauth_* tables and
// their lookup indexes exist. Re-running the DEFINE statements is a no-op.
func NewSurrealOAuthStore(db *surrealdb.DB, logger arbor.ILogger) *SurrealOAuthStore {
	s := &SurrealOAuthStore{db: db, logger: logger}
	ctx := context.Background()
	stmts := []string{
		"DEFINE TABLE IF NOT EXISTS oauth_client SCHEMALESS",
		"DEFINE TABLE IF NOT EXISTS oauth_session SCHEMALESS",
		"DEFINE TABLE IF NOT EXISTS oauth_code SCHEMALESS",
		"DEFINE TABLE IF NOT EXISTS oauth_refresh_token SCHEMALESS",
		"DEFINE INDEX IF NOT EXISTS oauth_session_client ON TABLE oauth_session FIELDS client_id",
		"DEFINE INDEX IF NOT EXISTS oauth_code_expires ON TABLE oauth_code FIELDS expires_at",
		"DEFINE INDEX IF NOT EXISTS oauth_rt_expires ON TABLE oauth_refresh_token FIELDS expires_at",
	}
	for _, sql := range stmts {
		_, _ = surrealdb.Query[any](ctx, db, sql, nil)
	}
	return s
}

// --- Clients ---

func (s *SurrealOAuthStore) SaveClient(ctx context.Context, c *OAuthClient) error {
	sql := "UPSERT $rid CONTENT $client"
	vars := map[string]any{
		"rid":    surrealmodels.NewRecordID("oauth_client", c.ClientID),
		"client": c,
	}
	if _, err := surrealdb.Query[[]OAuthClient](ctx, s.db, sql, vars); err != nil {
		return fmt.Errorf("auth: save oauth client: %w", err)
	}
	return nil
}

func (s *SurrealOAuthStore) GetClient(ctx context.Context, clientID string) (*OAuthClient, error) {
	c, err := surrealdb.Select[OAuthClient](ctx, s.db, surrealmodels.NewRecordID("oauth_client", clientID))
	if err != nil {
		return nil, fmt.Errorf("auth: select oauth client: %w", err)
	}
	if c == nil {
		return nil, ErrOAuthNotFound
	}
	return c, nil
}

// --- Sessions ---

func (s *SurrealOAuthStore) SaveSession(ctx context.Context, sess *OAuthSession) error {
	sql := "UPSERT $rid CONTENT $sess"
	vars := map[string]any{
		"rid":  surrealmodels.NewRecordID("oauth_session", sess.SessionID),
		"sess": sess,
	}
	if _, err := surrealdb.Query[[]OAuthSession](ctx, s.db, sql, vars); err != nil {
		return fmt.Errorf("auth: save oauth session: %w", err)
	}
	return nil
}

func (s *SurrealOAuthStore) GetSession(ctx context.Context, sessionID string) (*OAuthSession, error) {
	sess, err := surrealdb.Select[OAuthSession](ctx, s.db, surrealmodels.NewRecordID("oauth_session", sessionID))
	if err != nil {
		return nil, fmt.Errorf("auth: select oauth session: %w", err)
	}
	if sess == nil {
		return nil, ErrOAuthNotFound
	}
	return sess, nil
}

func (s *SurrealOAuthStore) GetSessionByClientID(ctx context.Context, clientID string) (*OAuthSession, error) {
	sql := "SELECT * FROM oauth_session WHERE client_id = $cid ORDER BY created_at DESC LIMIT 1"
	vars := map[string]any{"cid": clientID}
	results, err := surrealdb.Query[[]OAuthSession](ctx, s.db, sql, vars)
	if err != nil {
		return nil, fmt.Errorf("auth: query oauth session by client_id: %w", err)
	}
	if results != nil && len(*results) > 0 && len((*results)[0].Result) > 0 {
		row := (*results)[0].Result[0]
		return &row, nil
	}
	return nil, ErrOAuthNotFound
}

func (s *SurrealOAuthStore) DeleteSession(ctx context.Context, sessionID string) error {
	sql := "DELETE $rid"
	vars := map[string]any{"rid": surrealmodels.NewRecordID("oauth_session", sessionID)}
	if _, err := surrealdb.Query[any](ctx, s.db, sql, vars); err != nil {
		return fmt.Errorf("auth: delete oauth session: %w", err)
	}
	return nil
}

// --- Codes ---

func (s *SurrealOAuthStore) SaveCode(ctx context.Context, c *OAuthCode) error {
	sql := "UPSERT $rid CONTENT $code"
	vars := map[string]any{
		"rid":  surrealmodels.NewRecordID("oauth_code", c.Code),
		"code": c,
	}
	if _, err := surrealdb.Query[[]OAuthCode](ctx, s.db, sql, vars); err != nil {
		return fmt.Errorf("auth: save oauth code: %w", err)
	}
	return nil
}

func (s *SurrealOAuthStore) GetCode(ctx context.Context, code string) (*OAuthCode, error) {
	sql := "SELECT * FROM oauth_code WHERE code = $code AND used = false AND expires_at > $now LIMIT 1"
	vars := map[string]any{
		"code": code,
		"now":  time.Now().UTC(),
	}
	results, err := surrealdb.Query[[]OAuthCode](ctx, s.db, sql, vars)
	if err != nil {
		return nil, fmt.Errorf("auth: query oauth code: %w", err)
	}
	if results != nil && len(*results) > 0 && len((*results)[0].Result) > 0 {
		row := (*results)[0].Result[0]
		return &row, nil
	}
	return nil, ErrOAuthNotFound
}

func (s *SurrealOAuthStore) MarkCodeUsed(ctx context.Context, code string) error {
	sql := "UPDATE oauth_code SET used = true WHERE code = $code"
	vars := map[string]any{"code": code}
	if _, err := surrealdb.Query[[]OAuthCode](ctx, s.db, sql, vars); err != nil {
		return fmt.Errorf("auth: mark oauth code used: %w", err)
	}
	return nil
}

// --- Refresh tokens ---

func (s *SurrealOAuthStore) SaveRefreshToken(ctx context.Context, t *OAuthRefreshToken) error {
	sql := "UPSERT $rid CONTENT $rt"
	vars := map[string]any{
		"rid": surrealmodels.NewRecordID("oauth_refresh_token", t.Token),
		"rt":  t,
	}
	if _, err := surrealdb.Query[[]OAuthRefreshToken](ctx, s.db, sql, vars); err != nil {
		return fmt.Errorf("auth: save refresh token: %w", err)
	}
	return nil
}

func (s *SurrealOAuthStore) GetRefreshToken(ctx context.Context, token string) (*OAuthRefreshToken, error) {
	sql := "SELECT * FROM oauth_refresh_token WHERE token = $tok AND expires_at > $now LIMIT 1"
	vars := map[string]any{
		"tok": token,
		"now": time.Now().UTC(),
	}
	results, err := surrealdb.Query[[]OAuthRefreshToken](ctx, s.db, sql, vars)
	if err != nil {
		return nil, fmt.Errorf("auth: query refresh token: %w", err)
	}
	if results != nil && len(*results) > 0 && len((*results)[0].Result) > 0 {
		row := (*results)[0].Result[0]
		return &row, nil
	}
	return nil, ErrOAuthNotFound
}

func (s *SurrealOAuthStore) DeleteRefreshToken(ctx context.Context, token string) error {
	sql := "DELETE FROM oauth_refresh_token WHERE token = $tok"
	vars := map[string]any{"tok": token}
	if _, err := surrealdb.Query[any](ctx, s.db, sql, vars); err != nil {
		return fmt.Errorf("auth: delete refresh token: %w", err)
	}
	return nil
}

// Compile-time check.
var _ OAuthStore = (*SurrealOAuthStore)(nil)
