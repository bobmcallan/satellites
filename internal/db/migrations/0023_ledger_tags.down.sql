-- 0023_ledger_tags.down.sql — revert.

BEGIN;

ALTER TABLE api_keys DROP CONSTRAINT api_keys_role_check;
ALTER TABLE api_keys
    ADD CONSTRAINT api_keys_role_check CHECK (role IN ('executor', 'reviewer'));

DROP INDEX IF EXISTS evidence_run_created;
DROP INDEX IF EXISTS evidence_session_created;
DROP INDEX IF EXISTS evidence_workspace_created;
DROP INDEX IF EXISTS evidence_project_created;

ALTER TABLE evidence DROP COLUMN IF EXISTS run_id;
ALTER TABLE evidence DROP COLUMN IF EXISTS session_id;
ALTER TABLE evidence DROP COLUMN IF EXISTS workspace_id;
ALTER TABLE evidence DROP COLUMN IF EXISTS project_id;

-- Restoring NOT NULL needs all rows to have a story_id; this down
-- migration assumes that hasn't changed (no story_id-less rows landed
-- before the down migration ran).
ALTER TABLE evidence ALTER COLUMN story_id SET NOT NULL;

COMMIT;
