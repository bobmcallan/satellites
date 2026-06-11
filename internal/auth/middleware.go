package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/arbor"
)

// TouchLastSeenAsync records the user's last-seen time off the request path:
// a detached, time-boxed, best-effort write that never blocks or fails the
// request. The DB-side throttle keeps it to ~one write per minute per user.
func (s *Store) TouchLastSeenAsync(userID string) {
	if userID == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.TouchLastSeen(ctx, userID); err != nil {
			arbor.WarnCtx(ctx, "auth: touch last_seen", "user_id", userID, "err", err)
		}
	}()
}

type ctxKey struct{}
type apiKeyRoleCtxKey struct{}
type apiKeyProjectCtxKey struct{}
type transportCtxKey struct{}
type agentRoleCtxKey struct{}

// AgentRoleCap is a requested DOWNSCOPE on the caller's session (sty_3a1374b5):
// the caller asks its agent to operate at no more than Role, optionally
// restricted to Projects (empty = every project). It can only ATTENUATE —
// effectiveProjectRole takes min(actual, Role) and denies any project outside
// Projects. Because it never escalates, it is safe to accept from a client
// header without additional trust.
type AgentRoleCap struct {
	Role     string
	Projects []string
}

// WithAgentRole stamps a requested downscope cap onto ctx.
func WithAgentRole(ctx context.Context, cap AgentRoleCap) context.Context {
	return context.WithValue(ctx, agentRoleCtxKey{}, cap)
}

// AgentRoleFromContext returns the requested cap and whether one was set.
func AgentRoleFromContext(ctx context.Context) (AgentRoleCap, bool) {
	c, ok := ctx.Value(agentRoleCtxKey{}).(AgentRoleCap)
	return c, ok
}

// agentRoleFromHeader reads X-Satellites-Agent-Role (+ optional
// X-Satellites-Agent-Projects, comma-separated) and stamps a cap on ctx when a
// recognised role is present. A blank/unrecognised role leaves ctx unchanged
// (no cap → full user). Header-asserted is safe: the cap only attenuates.
func agentRoleFromHeader(ctx context.Context, r *http.Request) context.Context {
	role := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Satellites-Agent-Role")))
	switch role {
	case "read", "write", "admin":
	default:
		return ctx
	}
	var projects []string
	if p := strings.TrimSpace(r.Header.Get("X-Satellites-Agent-Projects")); p != "" {
		for _, part := range strings.Split(p, ",") {
			if s := strings.TrimSpace(part); s != "" {
				projects = append(projects, s)
			}
		}
	}
	return WithAgentRole(ctx, AgentRoleCap{Role: role, Projects: projects})
}

// Transport identifies how a verb call entered the process. It keeps
// operational content writes (document/skill/principle upsert) off the MCP
// surface while leaving the CLI exec path and internal calls untouched
// (sty_f302bd8b): content upload runs the local agent's review skill, which a
// server-side MCP write would bypass.
type Transport string

// TransportMCP marks a call that entered via the MCP tool surface.
const TransportMCP Transport = "mcp"

// WithTransport stamps the entry transport onto ctx. The MCP server sets
// TransportMCP before dispatching a tool; the HTTP exec handler and
// in-process callers leave it unset.
func WithTransport(ctx context.Context, t Transport) context.Context {
	return context.WithValue(ctx, transportCtxKey{}, t)
}

// TransportFromContext returns the entry transport, or "" when unset
// (CLI exec / in-process).
func TransportFromContext(ctx context.Context) Transport {
	t, _ := ctx.Value(transportCtxKey{}).(Transport)
	return t
}

// FromContext returns the authenticated user attached to the request
// context, or nil if unauthenticated.
func FromContext(ctx context.Context) *User {
	u, _ := ctx.Value(ctxKey{}).(*User)
	return u
}

// WithUser attaches a user to ctx the same way Middleware does.
// Exported for tests and for in-process callers that have already
// authenticated through a non-HTTP channel (e.g. CLI dispatching
// server-side verbs in-process during dev).
func WithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

// WithAPIKeyRole stamps the api-key role onto ctx. Middleware sets
// this on the api-key auth path so the verb layer can gate
// status/ledger writes on executor-vs-reviewer without re-querying.
// JWT and CLI-local paths leave the role unset; verbs treat absence
// as "not behind an api-key gate" (humans + internal calls pass).
func WithAPIKeyRole(ctx context.Context, role APIKeyRole) context.Context {
	return context.WithValue(ctx, apiKeyRoleCtxKey{}, role)
}

// APIKeyRoleFromContext returns the api-key role attached to ctx, or
// the zero value when none is set. Verbs distinguish three states:
// empty (not api-key-authed — bypass), executor (gate trips), reviewer
// (gate passes).
func APIKeyRoleFromContext(ctx context.Context) APIKeyRole {
	r, _ := ctx.Value(apiKeyRoleCtxKey{}).(APIKeyRole)
	return r
}

// WithAPIKeyProject stamps the api-key's bound project_id onto ctx.
// Middleware sets this on the api-key auth path so the project-role
// resolver (effectiveProjectRole) can map an executor/runner key onto
// `write` for the project it is bound to (epic:user-admin, sty_c96cc77f).
// Empty when the key is unbound or for JWT / CLI-local callers.
func WithAPIKeyProject(ctx context.Context, projectID string) context.Context {
	return context.WithValue(ctx, apiKeyProjectCtxKey{}, projectID)
}

// APIKeyProjectFromContext returns the api-key's bound project_id, or ""
// when none is set.
func APIKeyProjectFromContext(ctx context.Context) string {
	p, _ := ctx.Value(apiKeyProjectCtxKey{}).(string)
	return p
}

// SetJWTSecret installs the OAuth access-token signing secret on the
// Store. Once set, Middleware accepts JWT bearer tokens minted by the
// OAuth Authorization Server in addition to api-keys.
func (s *Store) SetJWTSecret(secret []byte) { s.jwtSecret = secret }

// Middleware authenticates incoming HTTP requests. Accepts two
// credential shapes on the Authorization: Bearer header:
//
//   - JWT — signed by the OAuth Authorization Server; sub claim
//     resolves to a user row.
//   - api-key — looked up in the api_keys table by SHA-256 hash.
//
// On success, attaches the user to the request context. On failure,
// returns 401 with WWW-Authenticate pointing at the discovery endpoint.
//
// Paths under /oauth/ are skipped — they implement their own auth flow.
func (s *Store) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/oauth/") {
			next.ServeHTTP(w, r)
			return
		}

		h := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(h, prefix) {
			setWWWAuthenticate(w, r)
			http.Error(w, "missing bearer credential", http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(h, prefix)

		// JWT path: minted by the OAuth AS. Identified by 3-segment dot
		// shape and a configured signing secret.
		if len(s.jwtSecret) > 0 && LooksLikeJWT(token) {
			claims, err := ValidateJWT(token, s.jwtSecret)
			if err != nil {
				setWWWAuthenticate(w, r)
				http.Error(w, "invalid credential", http.StatusUnauthorized)
				return
			}
			u, err := s.GetUserByID(r.Context(), claims.Sub)
			if err != nil {
				// JWT sub doesn't map to a known user — minimal identity
				// from the claims is still better than failing the
				// request, since the JWT itself was signature-verified.
				u = &User{
					ID:    claims.Sub,
					Email: claims.Email,
				}
			}
			ctx := WithUser(r.Context(), u)
			ctx = agentRoleFromHeader(ctx, r)
			r = r.WithContext(ctx)
			s.TouchLastSeenAsync(u.ID)
			next.ServeHTTP(w, r)
			return
		}

		// api-key path.
		u, role, keyProject, err := s.ValidateKeyWithRole(r.Context(), token)
		if err != nil {
			if errors.Is(err, ErrInvalidKey) {
				setWWWAuthenticate(w, r)
				http.Error(w, "invalid credential", http.StatusUnauthorized)
				return
			}
			arbor.ErrorCtx(r.Context(), "auth: validate key", "err", err)
			http.Error(w, "auth error", http.StatusInternalServerError)
			return
		}

		ctx := WithUser(r.Context(), u)
		ctx = WithAPIKeyRole(ctx, role)
		ctx = WithAPIKeyProject(ctx, keyProject)
		ctx = agentRoleFromHeader(ctx, r)
		r = r.WithContext(ctx)
		s.TouchLastSeenAsync(u.ID)
		next.ServeHTTP(w, r)
	})
}

// setWWWAuthenticate points unauthenticated /mcp callers at the
// protected-resource metadata so MCP-SDK clients can discover the
// authorization server (RFC 9728).
func setWWWAuthenticate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", fmt.Sprintf(
		`Bearer resource_metadata="%s/.well-known/oauth-protected-resource"`,
		SchemeAndHost(r),
	))
}
