-- 0019_variables_system_scope.down.sql — restore the pre-system schema.
-- Any system-scoped rows would violate the restored constraints, so the
-- down path deletes them first. Operators with operator-tunable knobs
-- they care about must back them up out-of-band before rolling back.

BEGIN;

DELETE FROM variables WHERE scope = 'system';

DROP INDEX IF EXISTS variables_system_unique;

ALTER TABLE variables DROP CONSTRAINT variables_scope_coherence;
ALTER TABLE variables
    ADD CONSTRAINT variables_scope_coherence CHECK (
        (scope = 'workspace' AND project_id IS NULL) OR
        (scope = 'project'   AND project_id IS NOT NULL)
    );

ALTER TABLE variables ALTER COLUMN workspace_id SET NOT NULL;

ALTER TABLE variables DROP CONSTRAINT variables_scope_check;
ALTER TABLE variables
    ADD CONSTRAINT variables_scope_check CHECK (scope IN ('workspace','project'));

COMMIT;
