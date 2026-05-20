-- 0002_auth.up.sql — users + api_keys.
-- See sty_9b3e355c. OAuth-issued users carry (oauth_provider, oauth_sub);
-- dev-mode seeded users carry only an email.

CREATE TABLE users (
    id              TEXT PRIMARY KEY,
    email           TEXT NOT NULL UNIQUE,
    display_name    TEXT NOT NULL DEFAULT '',
    oauth_provider  TEXT,
    oauth_sub       TEXT,
    role            TEXT NOT NULL DEFAULT 'user',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX users_oauth_id
    ON users (oauth_provider, oauth_sub)
    WHERE oauth_provider IS NOT NULL;

CREATE TABLE api_keys (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    project_id   TEXT,
    agent_name   TEXT NOT NULL DEFAULT '',
    key_hash     TEXT NOT NULL UNIQUE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at   TIMESTAMPTZ
);

CREATE INDEX api_keys_user ON api_keys (user_id);
CREATE UNIQUE INDEX api_keys_project_agent
    ON api_keys (user_id, project_id, agent_name)
    WHERE project_id IS NOT NULL;
