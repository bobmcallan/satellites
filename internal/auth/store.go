// Package auth provides V5's authentication primitives: users,
// api-keys, OAuth scaffolding, and the HTTP middleware that gates
// substrate verbs.
//
// The store is DB-backed against the users + api_keys tables (migration
// 0002). Api-keys are stored as SHA-256 hashes — high-entropy secrets
// don't need bcrypt and need to be O(1) lookupable by hash.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Role enumerates the V5 user-role surface.
type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

// APIKeyRole enumerates the per-key role surface added by sty_25d5b21e.
// Executor keys can edit story bodies; reviewer keys are the only ones
// permitted to patch story status or append ledger entries. The split
// is structural so the review subprocess can mint a short-lived
// reviewer key, do its work, and revoke — without ever handing the
// executor the ability to forge a transition.
type APIKeyRole string

const (
	APIKeyRoleExecutor APIKeyRole = "executor"
	APIKeyRoleReviewer APIKeyRole = "reviewer"
	// APIKeyRoleRunner is a third role minted for the `satellites story
	// run` driver (sty_7af47a91). A runner key may append log-kind
	// ledger rows so the driver's captured `claude -p` output can land
	// in the substrate ledger without the broader reviewer permissions
	// (status patching, review_finding kinds, etc.).
	APIKeyRoleRunner APIKeyRole = "runner"
)

// ReviewerAgentName is the reserved agent slot every reviewer key
// occupies. Reviewer keys are an ephemeral single-slot-per-(user,project)
// credential the gate rotates each run; pinning them to one reserved
// agent_name keeps them in their own row in the api_keys_project_agent
// unique index, separate from the executor key's real agent name, so a
// reviewer rotation can never clobber an executor key (sty_e2dea1ec).
const ReviewerAgentName = "reviewer"

// User is a satellites operator.
type User struct {
	ID          string
	Email       string
	DisplayName string
	Role        Role
	CreatedAt   time.Time
}

// APIKey is a credential authenticating HTTP/MCP requests.
//
// Role is `executor` (default) or `reviewer`. ExpiresAt is set only
// for short-lived reviewer keys minted by the review subprocess;
// ValidateKey filters out rows past their expiry so revocation is
// implicit when the TTL elapses.
type APIKey struct {
	ID        string
	UserID    string
	ProjectID string
	AgentName string
	Role      APIKeyRole
	KeyHash   string
	CreatedAt time.Time
	ExpiresAt *time.Time
	RevokedAt *time.Time
}

// ErrInvalidKey is returned by ValidateKey when the credential does
// not match any active row.
var ErrInvalidKey = errors.New("auth: invalid api key")

// Store wraps DB-backed auth operations.
type Store struct {
	DB        *sql.DB
	jwtSecret []byte
}

// New returns a Store bound to the given database/sql handle.
func New(db *sql.DB) *Store { return &Store{DB: db} }

// HashKey returns the deterministic hash used to store + look up api
// keys (SHA-256 hex).
func HashKey(rawKey string) string {
	h := sha256.Sum256([]byte(rawKey))
	return hex.EncodeToString(h[:])
}

func generateRawKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: rng: %w", err)
	}
	return "sk_" + hex.EncodeToString(b), nil
}

// CreateUser inserts a user idempotently on email; returns the user
// row (existing or new).
func (s *Store) CreateUser(ctx context.Context, id, email, displayName string, role Role) (*User, error) {
	if email == "" {
		return nil, fmt.Errorf("auth: email required")
	}
	if role == "" {
		role = RoleUser
	}
	if _, err := s.DB.ExecContext(ctx, `
        INSERT INTO users (id, email, display_name, role)
        VALUES ($1, $2, $3, $4)
        ON CONFLICT (email) DO NOTHING
    `, id, email, displayName, string(role)); err != nil {
		return nil, fmt.Errorf("auth: insert user: %w", err)
	}
	return s.GetUserByEmail(ctx, email)
}

// GetUserByEmail returns the user with the given email or sql.ErrNoRows.
func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	var u User
	if err := s.DB.QueryRowContext(ctx, `
        SELECT id, email, display_name, role, created_at
          FROM users WHERE email = $1
    `, email).Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.CreatedAt); err != nil {
		return nil, err
	}
	return &u, nil
}

// UpsertOAuthUser is idempotent on (provider, sub). On first sight it
// inserts a new user row (id = `usr_oauth_<sub>` prefixed by provider
// short code). On a re-callback it refreshes email + display_name and
// returns the existing row.
//
// If the user's email is contained in adminEmails (case-insensitive),
// the role is promoted to RoleAdmin — solving the prod bootstrap
// without manual SQL. Demotion is NOT automatic: a row already at
// RoleAdmin stays admin even if the email is removed from the env list.
// That's the V4 lesson — silent demotions are worse than stale admins.
func (s *Store) UpsertOAuthUser(ctx context.Context, provider, sub, email, displayName string, adminEmails []string) (*User, error) {
	if provider == "" || sub == "" {
		return nil, fmt.Errorf("auth: oauth upsert needs provider+sub")
	}
	if email == "" {
		return nil, fmt.Errorf("auth: oauth upsert needs email")
	}

	wantAdmin := false
	emailLC := strings.ToLower(strings.TrimSpace(email))
	for _, a := range adminEmails {
		if strings.ToLower(strings.TrimSpace(a)) == emailLC {
			wantAdmin = true
			break
		}
	}

	// Try update by (provider, sub). On hit, we're done.
	row := s.DB.QueryRowContext(ctx, `
        UPDATE users
           SET email        = $1,
               display_name = COALESCE(NULLIF($2, ''), display_name),
               role         = CASE WHEN $3 THEN 'admin' ELSE role END
         WHERE oauth_provider = $4 AND oauth_sub = $5
        RETURNING id, email, display_name, role, created_at
    `, email, displayName, wantAdmin, provider, sub)
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.CreatedAt)
	if err == nil {
		return &u, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("auth: oauth update: %w", err)
	}

	// No row for (provider, sub) yet. Insert. The unique index on email
	// means a pre-existing email-only user (e.g. dev-seeded admin@dev)
	// blocks insert — we surface that as a clean error rather than
	// silently linking, which would be a confusing identity merge.
	role := RoleUser
	if wantAdmin {
		role = RoleAdmin
	}
	id := fmt.Sprintf("usr_oauth_%s_%s", provider, sub)
	if _, err := s.DB.ExecContext(ctx, `
        INSERT INTO users (id, email, display_name, role, oauth_provider, oauth_sub)
        VALUES ($1, $2, $3, $4, $5, $6)
    `, id, email, displayName, string(role), provider, sub); err != nil {
		return nil, fmt.Errorf("auth: oauth insert: %w", err)
	}
	return s.GetUserByID(ctx, id)
}

// ListAPIKeysForUser returns the non-revoked api-keys owned by userID,
// ordered newest-first. Used by the /settings/api-keys UI.
func (s *Store) ListAPIKeysForUser(ctx context.Context, userID string) ([]APIKey, error) {
	rows, err := s.DB.QueryContext(ctx, `
        SELECT id, user_id, COALESCE(project_id,''), agent_name, role, key_hash, created_at, expires_at, revoked_at
          FROM api_keys
         WHERE user_id = $1 AND revoked_at IS NULL
         ORDER BY created_at DESC
    `, userID)
	if err != nil {
		return nil, fmt.Errorf("auth: list keys: %w", err)
	}
	defer rows.Close()
	var out []APIKey
	for rows.Next() {
		var k APIKey
		var role string
		if err := rows.Scan(&k.ID, &k.UserID, &k.ProjectID, &k.AgentName, &role, &k.KeyHash, &k.CreatedAt, &k.ExpiresAt, &k.RevokedAt); err != nil {
			return nil, fmt.Errorf("auth: scan key: %w", err)
		}
		k.Role = APIKeyRole(role)
		out = append(out, k)
	}
	return out, rows.Err()
}

// RevokeKeyForUser revokes id only when it belongs to userID — prevents
// one user revoking another's key by guessing the id.
func (s *Store) RevokeKeyForUser(ctx context.Context, userID, id string) error {
	res, err := s.DB.ExecContext(ctx, `
        UPDATE api_keys SET revoked_at = now()
         WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL
    `, id, userID)
	if err != nil {
		return fmt.Errorf("auth: revoke: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("auth: apikey %s not found or not yours", id)
	}
	return nil
}

// GetUserByID returns the user with the given id or sql.ErrNoRows. Used
// by the session middleware to resolve the cookie's user_id to a row.
func (s *Store) GetUserByID(ctx context.Context, id string) (*User, error) {
	var u User
	if err := s.DB.QueryRowContext(ctx, `
        SELECT id, email, display_name, role, created_at
          FROM users WHERE id = $1
    `, id).Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.CreatedAt); err != nil {
		return nil, err
	}
	return &u, nil
}

// IssueAPIKey mints an `executor`-role api-key (the only role
// available to operators via the apikey_create verb). Reviewer keys
// go through IssueReviewerKey, which is server-internal.
func (s *Store) IssueAPIKey(ctx context.Context, id, userID, projectID, agentName string) (string, *APIKey, error) {
	rawKey, err := generateRawKey()
	if err != nil {
		return "", nil, err
	}
	return s.issueWithRaw(ctx, id, userID, projectID, agentName, rawKey, APIKeyRoleExecutor, nil)
}

// IssueAPIKeyWithRaw inserts an executor-role api-key with a
// caller-supplied raw value. Used by DevSeed to install predictable
// keys. Idempotent on the key_hash unique index — a re-insert with
// the same raw key returns the existing row.
func (s *Store) IssueAPIKeyWithRaw(ctx context.Context, id, userID, projectID, agentName, rawKey string) (string, *APIKey, error) {
	return s.issueWithRaw(ctx, id, userID, projectID, agentName, rawKey, APIKeyRoleExecutor, nil)
}

// IssueReviewerKeyInput is the server-internal mint surface for the
// short-lived reviewer key the review subprocess wields. TTL is
// caller-supplied so the orchestrator can pick the value from
// server config; zero defaults to 5 minutes per the story.
type IssueReviewerKeyInput struct {
	UserID    string
	ProjectID string
	AgentName string
	TTL       time.Duration
}

// IssueReviewerKey mints a `reviewer`-role key with an expiry set
// from TTL. The verb-layer apikey_create cannot mint this role
// (admin-gated); this is the path the review subprocess uses
// server-internally. Mint/revoke audit-trail belongs to the caller
// (the verb layer appends to ledger so auth stays narrow).
func (s *Store) IssueReviewerKey(ctx context.Context, in IssueReviewerKeyInput) (string, *APIKey, error) {
	if strings.TrimSpace(in.UserID) == "" {
		return "", nil, fmt.Errorf("auth: reviewer key: user_id required")
	}
	ttl := in.TTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	rawKey, err := generateRawKey()
	if err != nil {
		return "", nil, err
	}
	agentName := strings.TrimSpace(in.AgentName)
	if agentName == "" {
		agentName = ReviewerAgentName
	}
	id := fmt.Sprintf("apk_rev_%s", randomKeyIDSuffixAuth())
	expires := time.Now().UTC().Add(ttl)
	return s.issueWithRaw(ctx, id, in.UserID, in.ProjectID, agentName, rawKey, APIKeyRoleReviewer, &expires)
}

// issueWithRaw is the single insert path for both executor and
// reviewer keys. expiresAt is nil for executor keys; reviewer keys
// always carry a value.
func (s *Store) issueWithRaw(ctx context.Context, id, userID, projectID, agentName, rawKey string, role APIKeyRole, expiresAt *time.Time) (string, *APIKey, error) {
	hash := HashKey(rawKey)

	var projectArg any
	if projectID != "" {
		projectArg = projectID
	}

	// A project-scoped key occupies a single slot per (user, project,
	// agent_name) tracked by the api_keys_project_agent unique index — which
	// counts a row even after it is revoked or expired. Re-minting such a key
	// must ROTATE THE SLOT IN PLACE rather than collide: the prior row's
	// hash / expiry / revoked_at are overwritten so a fresh token takes the
	// slot. This covers both the reviewer key the gate rotates every run
	// (sty_e2dea1ec) AND a project-scoped executor (bootstrap) key re-minted
	// after a revoke (sty_03d714c2) — the collision vire hit. One row per
	// slot, no accumulation of dead rows.
	//
	// A NULL-project executor key (the DevSeed admin/user keys) is not in that
	// partial index, so it keeps the insert-or-noop path on key_hash — a
	// DevSeed re-insert with the same raw key stays idempotent.
	query := `
        INSERT INTO api_keys (id, user_id, project_id, agent_name, role, key_hash, expires_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7)
        ON CONFLICT (key_hash) DO NOTHING`
	if role == APIKeyRoleReviewer || projectID != "" {
		query = `
        INSERT INTO api_keys (id, user_id, project_id, agent_name, role, key_hash, expires_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7)
        ON CONFLICT (user_id, project_id, agent_name) WHERE project_id IS NOT NULL
        DO UPDATE SET id         = EXCLUDED.id,
                      role       = EXCLUDED.role,
                      key_hash   = EXCLUDED.key_hash,
                      expires_at = EXCLUDED.expires_at,
                      revoked_at = NULL,
                      created_at = now()`
	}
	if _, err := s.DB.ExecContext(ctx, query,
		id, userID, projectArg, agentName, string(role), hash, expiresAt); err != nil {
		return "", nil, fmt.Errorf("auth: insert apikey: %w", err)
	}

	var k APIKey
	var roleStr string
	if err := s.DB.QueryRowContext(ctx, `
        SELECT id, user_id, COALESCE(project_id,''), agent_name, role, key_hash, created_at, expires_at, revoked_at
          FROM api_keys WHERE key_hash = $1
    `, hash).Scan(&k.ID, &k.UserID, &k.ProjectID, &k.AgentName, &roleStr, &k.KeyHash, &k.CreatedAt, &k.ExpiresAt, &k.RevokedAt); err != nil {
		return "", nil, err
	}
	k.Role = APIKeyRole(roleStr)
	return rawKey, &k, nil
}

func randomKeyIDSuffixAuth() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// ValidateKey looks up the user for a given raw api-key. Returns
// ErrInvalidKey when the key is unknown, revoked, or expired.
//
// The role the key was minted under is not returned here — callers
// that need to gate on role (verb-layer enforcement) use
// ValidateKeyWithRole, which returns both. Keeping the plain shape
// stable means existing call sites continue to compile.
func (s *Store) ValidateKey(ctx context.Context, rawKey string) (*User, error) {
	u, _, err := s.ValidateKeyWithRole(ctx, rawKey)
	return u, err
}

// ValidateKeyWithRole is the role-aware counterpart to ValidateKey.
// Used by the HTTP middleware so the api-key's role can be stamped
// onto the request context for the verb layer.
func (s *Store) ValidateKeyWithRole(ctx context.Context, rawKey string) (*User, APIKeyRole, error) {
	hash := HashKey(rawKey)
	var u User
	var role string
	err := s.DB.QueryRowContext(ctx, `
        SELECT u.id, u.email, u.display_name, u.role, u.created_at, k.role
          FROM api_keys k
          JOIN users u ON u.id = k.user_id
         WHERE k.key_hash = $1
           AND k.revoked_at IS NULL
           AND (k.expires_at IS NULL OR k.expires_at > now())
    `, hash).Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.CreatedAt, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrInvalidKey
	}
	if err != nil {
		return nil, "", err
	}
	return &u, APIKeyRole(role), nil
}

// RevokeKey sets revoked_at on the api-key matching the given id.
func (s *Store) RevokeKey(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx, `
        UPDATE api_keys SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL
    `, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("auth: apikey %s not found or already revoked", id)
	}
	return nil
}
