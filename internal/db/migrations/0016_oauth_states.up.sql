-- 0016_oauth_states.up.sql — durable CSRF state registry for outbound
-- OAuth sign-in (Google, GitHub). The previous in-memory StateStore
-- evaporated whenever a Fly machine was recycled (auto-stop, rolling
-- deploy, scale-out), turning every callback that landed on a fresh
-- process into "invalid state".
CREATE TABLE oauth_states (
    state      TEXT PRIMARY KEY,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX oauth_states_expires_at ON oauth_states (expires_at);
