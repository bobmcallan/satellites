-- 0021_apikey_roles.up.sql — per-key role + expiry for the reviewer gate.
--
-- See sty_25d5b21e. The status-transition gate is structural: only
-- `reviewer`-role keys can patch `documents.status` on story rows or
-- append `ledger`. The executor key the agent holds is body-only.
-- `expires_at` carries the short-lived reviewer-key TTL the future
-- review subprocess relies on; ValidateKey filters expired rows.
--
-- Existing rows default to `executor` per the story's migration AC.

BEGIN;

ALTER TABLE api_keys
    ADD COLUMN role TEXT NOT NULL DEFAULT 'executor'
        CHECK (role IN ('executor', 'reviewer'));

ALTER TABLE api_keys
    ADD COLUMN expires_at TIMESTAMPTZ;

CREATE INDEX api_keys_role ON api_keys (role);

COMMIT;
