-- 0009_oauth.up.sql — OAuth 2.1 Authorization Server tables.
-- See internal/auth/oauth_server.go for the flow. The TTL columns
-- (sessions.created_at, codes.expires_at, refresh_tokens.expires_at)
-- are checked at read-time; no background sweeper here yet.

CREATE TABLE oauth_clients (
    client_id                  TEXT PRIMARY KEY,
    client_secret              TEXT NOT NULL DEFAULT '',
    client_name                TEXT NOT NULL DEFAULT '',
    redirect_uris              TEXT[] NOT NULL,
    grant_types                TEXT[] NOT NULL,
    response_types             TEXT[] NOT NULL,
    token_endpoint_auth_method TEXT NOT NULL DEFAULT 'none',
    created_at                 BIGINT NOT NULL
);

CREATE TABLE oauth_sessions (
    session_id     TEXT PRIMARY KEY,
    client_id      TEXT NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    redirect_uri   TEXT NOT NULL,
    state          TEXT NOT NULL DEFAULT '',
    code_challenge TEXT NOT NULL,
    code_method    TEXT NOT NULL,
    scope          TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX oauth_sessions_client ON oauth_sessions (client_id, created_at DESC);

CREATE TABLE oauth_codes (
    code           TEXT PRIMARY KEY,
    client_id      TEXT NOT NULL,
    user_id        TEXT NOT NULL,
    redirect_uri   TEXT NOT NULL,
    code_challenge TEXT NOT NULL,
    scope          TEXT NOT NULL DEFAULT '',
    expires_at     TIMESTAMPTZ NOT NULL,
    used           BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE TABLE oauth_refresh_tokens (
    token       TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL,
    client_id   TEXT NOT NULL,
    scope       TEXT NOT NULL DEFAULT '',
    expires_at  TIMESTAMPTZ NOT NULL
);

CREATE INDEX oauth_refresh_tokens_user ON oauth_refresh_tokens (user_id, expires_at);
