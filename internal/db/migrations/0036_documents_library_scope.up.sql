-- 0036: library scope — the shared skill-distribution surface
-- (epic:skill-library, sty_5678cc4b).
--
-- A library row is a published artifact readable by every authenticated
-- caller and writable only into the caller's own publisher namespace.
-- Publisher = the publishing project, so identity is (project_id, name);
-- workspace_id stays NULL — the row deliberately escapes the workspace
-- boundary, which is the point of the library. Follows the user-scope
-- precedent (0024): widen the scope domain, restate coherence, one new
-- partial unique index. Existing rows are untouched.

ALTER TABLE documents DROP CONSTRAINT IF EXISTS documents_scope_check;
ALTER TABLE documents ADD CONSTRAINT documents_scope_check
    CHECK (scope IN ('system','workspace','project','user','library'));

ALTER TABLE documents DROP CONSTRAINT documents_scope_coherence;
ALTER TABLE documents ADD CONSTRAINT documents_scope_coherence CHECK (
    (scope = 'system'    AND workspace_id IS NULL     AND project_id IS NULL     AND user_id IS NULL)     OR
    (scope = 'workspace' AND workspace_id IS NOT NULL AND project_id IS NULL     AND user_id IS NULL)     OR
    (scope = 'project'   AND workspace_id IS NOT NULL AND project_id IS NOT NULL AND user_id IS NULL)     OR
    (scope = 'user'      AND workspace_id IS NULL     AND project_id IS NULL     AND user_id IS NOT NULL) OR
    (scope = 'library'   AND workspace_id IS NULL     AND project_id IS NOT NULL AND user_id IS NULL)
);

-- audience is the future door for restricted libraries: the read path
-- filters on it, and every row defaults to 'all'. No grant model ships
-- with this column — a non-'all' row is readable only by its publisher.
ALTER TABLE documents ADD COLUMN audience TEXT NOT NULL DEFAULT 'all';

-- Marketplace identity: one row per (publisher, name) across the shared
-- document/skill namespace, mirroring the other per-scope indexes.
CREATE UNIQUE INDEX documents_library_unique
    ON documents (project_id, name) WHERE scope = 'library' AND type IN ('document','skill');

-- Locator for the library name lookup.
CREATE INDEX documents_library_locator ON documents (scope, project_id, name);
