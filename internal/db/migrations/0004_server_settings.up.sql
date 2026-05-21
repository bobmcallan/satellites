-- 0004_server_settings.up.sql — durable key/value for server-managed
-- secrets (currently the session HMAC secret). Backing this in the DB
-- means signed-cookie sessions survive container restarts and rolling
-- deploys (fly.io etc.) — operators don't have to set an env var to
-- avoid logging everyone out on every boot.

CREATE TABLE server_settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
