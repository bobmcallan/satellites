-- 0018_documents_type_task.up.sql — add 'task' as a third documents.type
-- discriminator alongside 'document' and 'story'. Unblocks the story-
-- panel task sub-table (sty_90565ac7); the panel can range over a
-- per-story task chain stored under the same documents row shape.
--
-- A task is a documents row with:
--   - type        = 'task'
--   - scope       = 'project'
--   - parent_id   = the sty_* id of the story it belongs to
--
-- No task-specific columns are added in this migration — the existing
-- documents column set (name as title, tags, status, priority, parent_id
-- + the document_versions body) already covers what the panel needs to
-- render. Extra fields (kind, duration, claimed_by, etc.) can be added
-- as needs surface.

BEGIN;

-- Drop and re-add the type CHECK constraint to include 'task'. Postgres
-- requires the old constraint to go before the new one can take its
-- place under the same column.
ALTER TABLE documents DROP CONSTRAINT documents_type_check;
ALTER TABLE documents
    ADD CONSTRAINT documents_type_check CHECK (type IN ('document','story','task'));

COMMIT;
