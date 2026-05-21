// Package auth's oauth_server.go implements the MCP-spec OAuth 2.1
// Authorization Server (RFC 6749 + 7591 DCR + 7636 PKCE + 8414 AS
// metadata + 9728 protected-resource metadata).
//
// The flow:
//
//  1. Client hits /mcp → 401 + WWW-Authenticate: Bearer resource_metadata=…
//
//  2. Client GETs /.well-known/oauth-protected-resource (HandleProtectedResource — oauth_discovery.go)
//
//  3. Client GETs /.well-known/oauth-authorization-server (HandleAuthorizationServer — oauth_discovery.go)
//
//  4. Client POSTs /oauth/register (HandleRegister) — RFC 7591 DCR
//
//  5. Client redirects user to GET /oauth/authorize?… (HandleAuthorize)
//
//  6. OAuthServer creates an OAuthSession, sets mcp_session_id cookie, redirects to /
//
//  7. User completes browser login (handled by internal/server/login.go)
//
//  8. Login handler detects mcp_session_id, calls CompleteAuthorization
//
//  9. Client POSTs /oauth/token with code+verifier (HandleToken) — auth-code grant
//
//  10. OAuthServer mints JWT access + opaque refresh, returns JSON
//
//  11. Client uses Bearer JWT on /mcp (validated by Middleware)
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OAuthServerConfig is what main.go hands NewOAuthServer at boot.
type OAuthServerConfig struct {
	JWTSecret       []byte
	Issuer          string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	CodeTTL         time.Duration
	Store           OAuthStore
	DevMode         bool

	// ResolveSessionUser inspects an incoming /oauth/authorize request
	// and returns the already-logged-in user id (or "" when not). When
	// non-nil AND it returns a user id, /oauth/authorize short-circuits
	// straight into CompleteAuthorization, skipping the
	// mcp_session_id cookie + portal-bounce dance.
	ResolveSessionUser func(r *http.Request) string

	// UserByID stamps identity claims (email/display name) onto the
	// access-token JWT at mint time. Optional — when nil, JWTs carry
	// only sub/scope/client_id.
	UserByID func(ctx context.Context, userID string) (*User, error)
}

// OAuthServer is the MCP-spec OAuth 2.1 Authorization Server.
type OAuthServer struct {
	cfg OAuthServerConfig
}

// NewOAuthServer returns a configured server. Panics on missing
// required config — boot-time misconfiguration must be loud.
func NewOAuthServer(cfg OAuthServerConfig) *OAuthServer {
	if len(cfg.JWTSecret) == 0 {
		panic("auth: NewOAuthServer requires JWTSecret")
	}
	if cfg.Store == nil {
		panic("auth: NewOAuthServer requires Store")
	}
	if cfg.AccessTokenTTL <= 0 {
		cfg.AccessTokenTTL = 1 * time.Hour
	}
	if cfg.RefreshTokenTTL <= 0 {
		cfg.RefreshTokenTTL = 7 * 24 * time.Hour
	}
	if cfg.CodeTTL <= 0 {
		cfg.CodeTTL = 10 * time.Minute
	}
	cfg.Issuer = strings.TrimRight(strings.TrimSpace(cfg.Issuer), "/")
	return &OAuthServer{cfg: cfg}
}

// JWTSecret exposes the configured secret so the middleware can
// validate JWTs without a back-reference to OAuthServer.
func (s *OAuthServer) JWTSecret() []byte { return s.cfg.JWTSecret }

// --- Dynamic Client Registration (RFC 7591) ---

type dcrRequest struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

// HandleRegister handles POST /oauth/register. Public clients (PKCE +
// `token_endpoint_auth_method=none`) are the expected shape; we still
// generate a client_secret for clients that opt into confidential flows.
func (s *OAuthServer) HandleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req dcrRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "invalid JSON body")
		return
	}
	if len(req.RedirectURIs) == 0 {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "redirect_uris is required")
		return
	}

	clientID, err := generateUUID()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to generate client_id")
		return
	}
	clientSecret, err := generateRandomHex(32)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to generate client_secret")
		return
	}

	grantTypes := req.GrantTypes
	if len(grantTypes) == 0 {
		grantTypes = []string{"authorization_code", "refresh_token"}
	}
	responseTypes := req.ResponseTypes
	if len(responseTypes) == 0 {
		responseTypes = []string{"code"}
	}
	authMethod := req.TokenEndpointAuthMethod
	if authMethod == "" {
		authMethod = "none"
	}

	client := &OAuthClient{
		ClientID:                clientID,
		ClientSecret:            clientSecret,
		ClientName:              req.ClientName,
		RedirectURIs:            req.RedirectURIs,
		GrantTypes:              grantTypes,
		ResponseTypes:           responseTypes,
		TokenEndpointAuthMethod: authMethod,
		CreatedAt:               time.Now().Unix(),
	}
	if err := s.cfg.Store.SaveClient(r.Context(), client); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to register client")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(client)
}

// --- Authorization ---

// HandleAuthorize handles GET /oauth/authorize. Initiates a browser
// flow that ends with a redirect to redirect_uri carrying ?code=…&state=….
func (s *OAuthServer) HandleAuthorize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	clientID := q.Get("client_id")
	redirectURI := q.Get("redirect_uri")

	// Resilience for clients that drop the redirect_uri. If we have a
	// recent session for this client, resume it.
	if redirectURI == "" && clientID != "" {
		if sess, err := s.cfg.Store.GetSessionByClientID(r.Context(), clientID); err == nil {
			s.setSessionCookieAndRedirect(w, r, sess.SessionID)
			return
		}
		http.Error(w, "missing redirect_uri", http.StatusBadRequest)
		return
	}

	parsed, err := url.Parse(redirectURI)
	if err != nil || parsed.Host == "" {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}

	responseType := q.Get("response_type")
	codeChallenge := q.Get("code_challenge")
	codeChallengeMethod := q.Get("code_challenge_method")
	state := q.Get("state")
	scope := q.Get("scope")

	if clientID == "" || responseType == "" || codeChallenge == "" || codeChallengeMethod == "" || state == "" {
		oauthRedirectWithError(w, r, redirectURI, "invalid_request", "missing required parameters", state)
		return
	}
	if responseType != "code" {
		oauthRedirectWithError(w, r, redirectURI, "unsupported_response_type", "only code is supported", state)
		return
	}
	if codeChallengeMethod != "S256" {
		oauthRedirectWithError(w, r, redirectURI, "invalid_request", "only S256 code_challenge_method is supported", state)
		return
	}

	sessionID, err := s.createAuthSession(r.Context(), clientID, redirectURI, codeChallenge, codeChallengeMethod, state, scope)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.setSessionCookieAndRedirect(w, r, sessionID)
}

func (s *OAuthServer) createAuthSession(ctx context.Context, clientID, redirectURI, codeChallenge, codeChallengeMethod, state, scope string) (string, error) {
	client, err := s.cfg.Store.GetClient(ctx, clientID)
	if err != nil {
		// Auto-register so unknown clients can still complete a flow.
		client = &OAuthClient{
			ClientID:                clientID,
			ClientName:              "auto-registered",
			RedirectURIs:            []string{redirectURI},
			GrantTypes:              []string{"authorization_code", "refresh_token"},
			ResponseTypes:           []string{"code"},
			TokenEndpointAuthMethod: "none",
			CreatedAt:               time.Now().Unix(),
		}
		if err := s.cfg.Store.SaveClient(ctx, client); err != nil {
			return "", fmt.Errorf("auto-register client: %w", err)
		}
	}
	if !containsString(client.RedirectURIs, redirectURI) {
		return "", errors.New("redirect_uri does not match registered URI")
	}
	if scope == "" {
		scope = DefaultOAuthScope
	}

	sessionID, err := generateRandomHex(16)
	if err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	session := &OAuthSession{
		SessionID:     sessionID,
		ClientID:      clientID,
		RedirectURI:   redirectURI,
		State:         state,
		CodeChallenge: codeChallenge,
		CodeMethod:    codeChallengeMethod,
		Scope:         scope,
		CreatedAt:     time.Now().UTC(),
	}
	if err := s.cfg.Store.SaveSession(ctx, session); err != nil {
		return "", fmt.Errorf("save oauth session: %w", err)
	}
	return sessionID, nil
}

// setSessionCookieAndRedirect drops the mcp_session_id cookie that
// internal/server/login.go reads after a successful login, then sends
// the browser to the login page. When the user is already logged in,
// short-circuits via CompleteAuthorization.
func (s *OAuthServer) setSessionCookieAndRedirect(w http.ResponseWriter, r *http.Request, sessionID string) {
	if userID := s.extractSessionUserID(r); userID != "" {
		redirectURL, err := s.CompleteAuthorization(r.Context(), sessionID, userID)
		if err == nil {
			http.Redirect(w, r, redirectURL, http.StatusFound)
			return
		}
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "mcp_session_id",
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   !s.cfg.DevMode,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})
	http.Redirect(w, r, "/login?mcp_session="+sessionID, http.StatusFound)
}

func (s *OAuthServer) extractSessionUserID(r *http.Request) string {
	if s.cfg.ResolveSessionUser == nil {
		return ""
	}
	return s.cfg.ResolveSessionUser(r)
}

// CompleteAuthorization is called by the login handler when it detects
// the mcp_session_id bridge cookie on a successful login. Mints a
// single-use code, deletes the OAuthSession, returns the redirect URL
// (with ?code=…&state=…).
func (s *OAuthServer) CompleteAuthorization(ctx context.Context, sessionID, userID string) (string, error) {
	sess, err := s.cfg.Store.GetSession(ctx, sessionID)
	if err != nil {
		return "", fmt.Errorf("session not found or expired")
	}
	code, err := generateRandomHex(16)
	if err != nil {
		return "", fmt.Errorf("generate auth code: %w", err)
	}
	authCode := &OAuthCode{
		Code:          code,
		ClientID:      sess.ClientID,
		UserID:        userID,
		RedirectURI:   sess.RedirectURI,
		CodeChallenge: sess.CodeChallenge,
		Scope:         sess.Scope,
		ExpiresAt:     time.Now().Add(s.cfg.CodeTTL).UTC(),
		Used:          false,
	}
	if err := s.cfg.Store.SaveCode(ctx, authCode); err != nil {
		return "", fmt.Errorf("save auth code: %w", err)
	}
	_ = s.cfg.Store.DeleteSession(ctx, sessionID)

	redirectURL, err := url.Parse(sess.RedirectURI)
	if err != nil {
		return "", fmt.Errorf("invalid redirect URI: %w", err)
	}
	q := redirectURL.Query()
	q.Set("code", code)
	q.Set("state", sess.State)
	redirectURL.RawQuery = q.Encode()
	return redirectURL.String(), nil
}

// --- Token ---

func (s *OAuthServer) HandleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "invalid form body")
		return
	}
	switch r.FormValue("grant_type") {
	case "authorization_code":
		s.handleAuthCodeGrant(w, r)
	case "refresh_token":
		s.handleRefreshTokenGrant(w, r)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
	}
}

func (s *OAuthServer) handleAuthCodeGrant(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	code := r.FormValue("code")
	clientID := r.FormValue("client_id")
	redirectURI := r.FormValue("redirect_uri")
	codeVerifier := r.FormValue("code_verifier")

	if code == "" || clientID == "" || redirectURI == "" || codeVerifier == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "missing required parameters")
		return
	}

	authCode, err := s.cfg.Store.GetCode(ctx, code)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code not found, expired, or already used")
		return
	}
	if authCode.ClientID != clientID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "client_id mismatch")
		return
	}
	if authCode.RedirectURI != redirectURI {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri mismatch")
		return
	}
	if !VerifyPKCE(codeVerifier, authCode.CodeChallenge) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
		return
	}
	if err := s.cfg.Store.MarkCodeUsed(ctx, code); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to consume authorization code")
		return
	}

	issuer := s.issuerForRequest(r)
	accessToken, err := s.mintAccessToken(ctx, authCode.UserID, authCode.Scope, authCode.ClientID, issuer)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to mint access token")
		return
	}
	refreshToken, err := generateUUID()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to generate refresh token")
		return
	}
	rt := &OAuthRefreshToken{
		Token:     refreshToken,
		UserID:    authCode.UserID,
		ClientID:  authCode.ClientID,
		Scope:     authCode.Scope,
		ExpiresAt: time.Now().Add(s.cfg.RefreshTokenTTL).UTC(),
	}
	_ = s.cfg.Store.SaveRefreshToken(ctx, rt)

	writeOAuthTokenJSON(w, accessToken, refreshToken, authCode.Scope, s.cfg.AccessTokenTTL)
}

func (s *OAuthServer) handleRefreshTokenGrant(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	oldToken := r.FormValue("refresh_token")
	clientID := r.FormValue("client_id")

	if oldToken == "" || clientID == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "missing required parameters")
		return
	}

	rt, err := s.cfg.Store.GetRefreshToken(ctx, oldToken)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token not found or expired")
		return
	}
	if rt.ClientID != clientID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "client_id mismatch")
		return
	}

	_ = s.cfg.Store.DeleteRefreshToken(ctx, oldToken)

	issuer := s.issuerForRequest(r)
	accessToken, err := s.mintAccessToken(ctx, rt.UserID, rt.Scope, rt.ClientID, issuer)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to mint access token")
		return
	}
	newToken, err := generateUUID()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to generate refresh token")
		return
	}
	newRT := &OAuthRefreshToken{
		Token:     newToken,
		UserID:    rt.UserID,
		ClientID:  rt.ClientID,
		Scope:     rt.Scope,
		ExpiresAt: time.Now().Add(s.cfg.RefreshTokenTTL).UTC(),
	}
	_ = s.cfg.Store.SaveRefreshToken(ctx, newRT)

	writeOAuthTokenJSON(w, accessToken, newToken, rt.Scope, s.cfg.AccessTokenTTL)
}

func (s *OAuthServer) mintAccessToken(ctx context.Context, userID, scope, clientID, issuer string) (string, error) {
	now := time.Now()
	claims := &JWTClaims{
		Sub:      userID,
		Scope:    scope,
		ClientID: clientID,
		Iss:      issuer,
		Iat:      now.Unix(),
		Exp:      now.Add(s.cfg.AccessTokenTTL).Unix(),
	}
	if s.cfg.UserByID != nil {
		if u, err := s.cfg.UserByID(ctx, userID); err == nil && u != nil {
			claims.Email = u.Email
			claims.Name = u.DisplayName
		}
	}
	return CreateJWT(claims, s.cfg.JWTSecret)
}

// --- Helpers ---

func (s *OAuthServer) issuerForRequest(r *http.Request) string {
	if s.cfg.Issuer != "" {
		return s.cfg.Issuer
	}
	return SchemeAndHost(r)
}

func writeOAuthError(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             code,
		"error_description": description,
	})
}

func writeOAuthTokenJSON(w http.ResponseWriter, access, refresh, scope string, ttl time.Duration) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token":  access,
		"token_type":    "Bearer",
		"expires_in":    int64(ttl.Seconds()),
		"refresh_token": refresh,
		"scope":         scope,
	})
}

func oauthRedirectWithError(w http.ResponseWriter, r *http.Request, redirectURI, code, description, state string) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	q := u.Query()
	q.Set("error", code)
	q.Set("error_description", description)
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func generateRandomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func generateUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func containsString(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}
