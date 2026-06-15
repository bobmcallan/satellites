-- 0043_variables_user_scope.up.sql — user-scope override layer for variables
-- (epic:workspace-agents, sty_6cdf1cd0).
--
-- Adds a highest-precedence "user" scope to the variable cascade, mirroring the
-- documents user-scope layer (0024). A user override is a row keyed
-- (user_id, name) that shadows the same-named project/workspace/system variable
-- for that caller — so an individual can override any key-value (e.g.
-- workflow.max_iterations, model selection, page sizes) for themselves without
-- editing the shared rows. Additive + backward compatible: nullable column,
-- widened scope and coherence checks, one new partial unique index. Existing
-- rows (user_id NULL) are untouched.

BEGIN;

ALTER TABLE variables ADD COLUMN user_id TEXT REFERENCES users(id) ON DELETE CASCADE;

-- Widen the scope domain to include 'user'. 0019 named this variables_scope_check.
ALTER TABLE variables DROP CONSTRAINT variables_scope_check;
ALTER TABLE variables ADD CONSTRAINT variables_scope_check
    CHECK (scope IN ('system','workspace','project','user'));

-- Re-state scope coherence with the user case: a user row has user_id set and
-- workspace_id / project_id NULL; every other scope has user_id NULL.
ALTER TABLE variables DROP CONSTRAINT variables_scope_coherence;
ALTER TABLE variables ADD CONSTRAINT variables_scope_coherence CHECK (
    (scope = 'system'    AND workspace_id IS NULL     AND project_id IS NULL     AND user_id IS NULL)     OR
    (scope = 'workspace' AND workspace_id IS NOT NULL AND project_id IS NULL     AND user_id IS NULL)     OR
    (scope = 'project'   AND workspace_id IS NOT NULL AND project_id IS NOT NULL AND user_id IS NULL)     OR
    (scope = 'user'      AND workspace_id IS NULL     AND project_id IS NULL     AND user_id IS NOT NULL)
);

-- Uniqueness within the user scope: one row per (user_id, name).
CREATE UNIQUE INDEX variables_user_unique
    ON variables (user_id, name) WHERE scope = 'user';

-- Locator for the user-scope name lookup the cascade performs.
CREATE INDEX variables_user_locator ON variables (scope, user_id, name);

COMMIT;
