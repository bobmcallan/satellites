-- 0010_documents.up.sql — documents-as-substrate.
--
-- Two tables: documents (latest pointer + identity columns) and
-- document_versions (immutable history). Every upsert appends a new
-- document_versions row and advances documents.latest_version; nothing
-- updates a version row in place.
--
-- Scope is a typed column with three values:
--   - system    : ships embedded/seeded, read-only via user-facing verbs
--   - workspace : bound to a workspace, operator-editable
--   - project   : bound to a (workspace, project) pair, operator-editable
--
-- The scope-coherence CHECK keeps workspace_id / project_id columns
-- aligned with scope. Read-only-system enforcement is at the store
-- layer (ErrScopeReadonly) — DB-level RLS would also work but the
-- substrate doesn't otherwise use it yet.

CREATE TABLE documents (
    id              TEXT PRIMARY KEY,                                          -- doc_<8hex>
    scope           TEXT NOT NULL CHECK (scope IN ('system','workspace','project')),
    workspace_id    TEXT REFERENCES workspaces(id) ON DELETE CASCADE,
    project_id      TEXT REFERENCES projects(id)   ON DELETE CASCADE,
    name            TEXT NOT NULL,
    latest_version  INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT documents_scope_coherence CHECK (
        (scope = 'system'    AND workspace_id IS NULL     AND project_id IS NULL) OR
        (scope = 'workspace' AND workspace_id IS NOT NULL AND project_id IS NULL) OR
        (scope = 'project'   AND workspace_id IS NOT NULL AND project_id IS NOT NULL)
    )
);

-- Acceptance criterion 4: indices on (scope, name) and
-- (scope, workspace_id, project_id, name).
CREATE INDEX documents_scope_name ON documents (scope, name);
CREATE INDEX documents_locator    ON documents (scope, workspace_id, project_id, name);

-- Uniqueness within scope. Three partial indexes because NULLable
-- columns in a composite unique index don't deduplicate cleanly
-- across Postgres versions; this keeps the constraint explicit.
CREATE UNIQUE INDEX documents_system_unique
    ON documents (name) WHERE scope = 'system';
CREATE UNIQUE INDEX documents_workspace_unique
    ON documents (workspace_id, name) WHERE scope = 'workspace';
CREATE UNIQUE INDEX documents_project_unique
    ON documents (workspace_id, project_id, name) WHERE scope = 'project';

CREATE TABLE document_versions (
    document_id  TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    version      INTEGER NOT NULL CHECK (version >= 1),
    body         TEXT    NOT NULL DEFAULT '',
    status       TEXT    NOT NULL DEFAULT 'active' CHECK (status IN ('active','deleted')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by   TEXT    NOT NULL DEFAULT '',                                   -- user_id or 'system:seed'
    PRIMARY KEY (document_id, version)
);

CREATE INDEX document_versions_status ON document_versions (document_id, status);
