-- 0041_project_substrate_not_workspace_keyed.down.sql
-- Restore the workspace-keyed unique index shape. NOTE: the data changes (the
-- deduplicated duplicate rows and the original home workspace_id) are NOT
-- recoverable — the deleted rows are gone and the pre-null workspace_id was not
-- recorded; this down only restores the index definition.
DROP INDEX IF EXISTS documents_project_unique;
CREATE UNIQUE INDEX documents_project_unique
    ON documents (workspace_id, project_id, name) WHERE scope = 'project' AND type IN ('document','skill');

-- Restore the stricter scope coherence (project requires workspace_id NOT NULL).
-- NOTE: rows nulled by the up migration would violate this; the down is only
-- safe before project doc/skill rows have been nulled in anger.
ALTER TABLE documents DROP CONSTRAINT documents_scope_coherence;
ALTER TABLE documents ADD CONSTRAINT documents_scope_coherence CHECK (
    (scope = 'system'    AND workspace_id IS NULL     AND project_id IS NULL     AND user_id IS NULL)     OR
    (scope = 'workspace' AND workspace_id IS NOT NULL AND project_id IS NULL     AND user_id IS NULL)     OR
    (scope = 'project'   AND workspace_id IS NOT NULL AND project_id IS NOT NULL AND user_id IS NULL)     OR
    (scope = 'user'      AND workspace_id IS NULL     AND project_id IS NULL     AND user_id IS NOT NULL) OR
    (scope = 'library'   AND workspace_id IS NULL     AND project_id IS NOT NULL AND user_id IS NULL)
);
