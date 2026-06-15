-- 0043_variables_user_scope.down.sql — reverse the user-scope override layer.

BEGIN;

-- Remove any user-scope rows first so the narrowed coherence check applies.
DELETE FROM variables WHERE scope = 'user';

DROP INDEX IF EXISTS variables_user_locator;
DROP INDEX IF EXISTS variables_user_unique;

ALTER TABLE variables DROP CONSTRAINT variables_scope_coherence;
ALTER TABLE variables ADD CONSTRAINT variables_scope_coherence CHECK (
    (scope = 'system'    AND workspace_id IS NULL     AND project_id IS NULL) OR
    (scope = 'workspace' AND workspace_id IS NOT NULL AND project_id IS NULL) OR
    (scope = 'project'   AND workspace_id IS NOT NULL AND project_id IS NOT NULL)
);

ALTER TABLE variables DROP CONSTRAINT variables_scope_check;
ALTER TABLE variables ADD CONSTRAINT variables_scope_check
    CHECK (scope IN ('system','workspace','project'));

ALTER TABLE variables DROP COLUMN user_id;

COMMIT;
