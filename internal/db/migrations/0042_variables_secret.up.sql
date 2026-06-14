-- 0042_variables_secret.up.sql — secret-typed key-values (epic:workspace-agents,
-- sty_a6983e32). LLM API keys move out of env-only into the variable store as
-- SECRET rows: admin-gated on write, never echoed by variable_get / variable_list.
--
-- The value is stored plaintext (Postgres is already the trust boundary via
-- DATABASE_URL); the guarantee is redaction at the read verbs, not encryption at
-- rest. A server-internal resolver reads the row directly to feed the LLM
-- clients; the public read verbs blank it.

BEGIN;

ALTER TABLE variables
    ADD COLUMN secret BOOLEAN NOT NULL DEFAULT false;

COMMIT;
