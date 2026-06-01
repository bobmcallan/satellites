-- 0024: user-scope override layer (sty_cbeeb452).
--
-- Adds a highest-precedence "user" scope to the documents cascade. A user
-- override is a row keyed (user_id, name) that shadows the same-named
-- project/workspace/system row for that caller — so a user can adapt the
-- process (workflow, gate, principle, config) without editing the shared
-- rows. Additive + backward compatible: nullable column, widened scope and
-- coherence checks, one new partial unique index. Existing rows (user_id
-- NULL) are untouched.

ALTER TABLE documents ADD COLUMN user_id TEXT REFERENCES users(id) ON DELETE CASCADE;

-- Widen the scope domain to include 'user'. The inline column CHECK from
-- 0010 is auto-named documents_scope_check.
ALTER TABLE documents DROP CONSTRAINT IF EXISTS documents_scope_check;
ALTER TABLE documents ADD CONSTRAINT documents_scope_check
    CHECK (scope IN ('system','workspace','project','user'));

-- Re-state scope coherence with the user case: a user row has user_id set
-- and workspace_id / project_id NULL; every other scope has user_id NULL.
ALTER TABLE documents DROP CONSTRAINT documents_scope_coherence;
ALTER TABLE documents ADD CONSTRAINT documents_scope_coherence CHECK (
    (scope = 'system'    AND workspace_id IS NULL     AND project_id IS NULL     AND user_id IS NULL)     OR
    (scope = 'workspace' AND workspace_id IS NOT NULL AND project_id IS NULL     AND user_id IS NULL)     OR
    (scope = 'project'   AND workspace_id IS NOT NULL AND project_id IS NOT NULL AND user_id IS NULL)     OR
    (scope = 'user'      AND workspace_id IS NULL     AND project_id IS NULL     AND user_id IS NOT NULL)
);

-- Uniqueness within the user scope: one row per (user_id, name) across the
-- shared document/skill namespace, mirroring the other per-scope indexes.
CREATE UNIQUE INDEX documents_user_unique
    ON documents (user_id, name) WHERE scope = 'user' AND type IN ('document','skill');

-- Locator for the user-scope name lookup the cascade performs.
CREATE INDEX documents_user_locator ON documents (scope, user_id, name);
