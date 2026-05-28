-- 0023_ledger_tags.up.sql — generalise the evidence ledger for
-- operator-observability (sty_0006f5f5).
--
-- The evidence table (migration 0001) was V5's append-only ledger
-- scoped strictly to stories: story_id NOT NULL, no project /
-- workspace / session / run correlation. Operator observability
-- needs an audit trail of arbor logs and verb calls across runs of
-- `claude -p`, which are not always story-scoped.
--
-- This migration:
--   - relaxes story_id to NULL-able (system / project / workspace-
--     scoped events have no story);
--   - adds the four correlation columns the HTTP context middleware
--     and arbor LedgerHandler tag every row with: project_id,
--     workspace_id, session_id, run_id;
--   - indexes each so the portal `/ledger` page can filter on any
--     combination without a sequential scan;
--   - extends the api_keys.role CHECK constraint to admit `runner`
--     so the `satellites story run` driver (sty_7af47a91) can mint
--     a key that may append log-kind ledger rows without needing
--     the broader reviewer role.
--
-- Append-only enforcement (evidence_no_update / evidence_no_delete
-- triggers from 0001) is unchanged — new columns are write-once like
-- the existing ones.

BEGIN;

-- Relax story_id: a log row from session-level activity (CLI bootstrap,
-- portal navigation) has no story.
ALTER TABLE evidence ALTER COLUMN story_id DROP NOT NULL;

-- Correlation columns. Defaults to empty string keep older code paths
-- (story-only events) writing cleanly; new code paths populate from
-- context.
ALTER TABLE evidence ADD COLUMN project_id   TEXT NOT NULL DEFAULT '';
ALTER TABLE evidence ADD COLUMN workspace_id TEXT NOT NULL DEFAULT '';
ALTER TABLE evidence ADD COLUMN session_id   TEXT NOT NULL DEFAULT '';
ALTER TABLE evidence ADD COLUMN run_id       TEXT NOT NULL DEFAULT '';

CREATE INDEX evidence_project_created   ON evidence (project_id, created_at);
CREATE INDEX evidence_workspace_created ON evidence (workspace_id, created_at);
CREATE INDEX evidence_session_created   ON evidence (session_id, created_at);
CREATE INDEX evidence_run_created       ON evidence (run_id, created_at);

-- Admit `runner` as a third api-key role. Postgres requires the old
-- constraint to drop before the new one takes its place under the same
-- column.
ALTER TABLE api_keys DROP CONSTRAINT api_keys_role_check;
ALTER TABLE api_keys
    ADD CONSTRAINT api_keys_role_check CHECK (role IN ('executor', 'reviewer', 'runner'));

COMMIT;
