-- 0036 down: drop the library scope. Library rows must go first or the
-- narrowed CHECK constraints cannot be applied.

DELETE FROM documents WHERE scope = 'library';

DROP INDEX IF EXISTS documents_library_locator;
DROP INDEX IF EXISTS documents_library_unique;

ALTER TABLE documents DROP COLUMN IF EXISTS audience;

ALTER TABLE documents DROP CONSTRAINT documents_scope_coherence;
ALTER TABLE documents ADD CONSTRAINT documents_scope_coherence CHECK (
    (scope = 'system'    AND workspace_id IS NULL     AND project_id IS NULL     AND user_id IS NULL)     OR
    (scope = 'workspace' AND workspace_id IS NOT NULL AND project_id IS NULL     AND user_id IS NULL)     OR
    (scope = 'project'   AND workspace_id IS NOT NULL AND project_id IS NOT NULL AND user_id IS NULL)     OR
    (scope = 'user'      AND workspace_id IS NULL     AND project_id IS NULL     AND user_id IS NOT NULL)
);

ALTER TABLE documents DROP CONSTRAINT IF EXISTS documents_scope_check;
ALTER TABLE documents ADD CONSTRAINT documents_scope_check
    CHECK (scope IN ('system','workspace','project','user'));
