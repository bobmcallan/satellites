-- Reverse 0024: drop the user-scope override layer.

-- Remove any user-scope rows first so the narrowed coherence check applies.
DELETE FROM documents WHERE scope = 'user';

DROP INDEX IF EXISTS documents_user_locator;
DROP INDEX IF EXISTS documents_user_unique;

ALTER TABLE documents DROP CONSTRAINT documents_scope_coherence;
ALTER TABLE documents ADD CONSTRAINT documents_scope_coherence CHECK (
    (scope = 'system'    AND workspace_id IS NULL     AND project_id IS NULL) OR
    (scope = 'workspace' AND workspace_id IS NOT NULL AND project_id IS NULL) OR
    (scope = 'project'   AND workspace_id IS NOT NULL AND project_id IS NOT NULL)
);

ALTER TABLE documents DROP CONSTRAINT IF EXISTS documents_scope_check;
ALTER TABLE documents ADD CONSTRAINT documents_scope_check
    CHECK (scope IN ('system','workspace','project'));

ALTER TABLE documents DROP COLUMN user_id;
