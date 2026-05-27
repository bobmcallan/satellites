-- 0019_variables_system_scope.up.sql — open the variables table to
-- scope='system' for stored, operator-tunable knobs.
--
-- Pre-existing system semantics (computed-at-request: version, os, arch,
-- server_url, …) are unchanged — those terminate at the verb-layer
-- resolver and never touch this table. The stored-system path adds a
-- separate row class for knobs like stories.page_size that need to
-- survive restart and be edited at runtime.
--
-- Schema deltas:
--  1. scope CHECK admits 'system'.
--  2. workspace_id becomes nullable (system rows have no workspace).
--  3. scope_coherence widens to allow (system, NULL, NULL).
--  4. New unique index on (name) WHERE scope='system' so seeds are
--     name-addressed.

BEGIN;

ALTER TABLE variables DROP CONSTRAINT variables_scope_check;
ALTER TABLE variables
    ADD CONSTRAINT variables_scope_check CHECK (scope IN ('system','workspace','project'));

ALTER TABLE variables ALTER COLUMN workspace_id DROP NOT NULL;

ALTER TABLE variables DROP CONSTRAINT variables_scope_coherence;
ALTER TABLE variables
    ADD CONSTRAINT variables_scope_coherence CHECK (
        (scope = 'system'    AND workspace_id IS NULL AND project_id IS NULL) OR
        (scope = 'workspace' AND workspace_id IS NOT NULL AND project_id IS NULL) OR
        (scope = 'project'   AND workspace_id IS NOT NULL AND project_id IS NOT NULL)
    );

CREATE UNIQUE INDEX variables_system_unique ON variables (name) WHERE scope = 'system';

COMMIT;
