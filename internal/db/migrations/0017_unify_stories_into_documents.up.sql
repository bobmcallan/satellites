-- 0017_unify_stories_into_documents.up.sql — collapse the stories
-- table into documents under a `type` discriminator. Stories are
-- documents-of-type-story; documents are documents-of-type-document.
-- One row shape, one filter surface, one list verb.
--
-- Atomic: schema → row move → FK repoint → drop. A single transaction
-- so a partial failure leaves the database untouched.

BEGIN;

------------------------------------------------------------------------
-- 1. Schema additions on documents.
------------------------------------------------------------------------

ALTER TABLE documents
    ADD COLUMN type                TEXT NOT NULL DEFAULT 'document'
              CHECK (type IN ('document','story')),
    ADD COLUMN tags                TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN status              TEXT NOT NULL DEFAULT 'active',
    ADD COLUMN priority            TEXT,
    ADD COLUMN category            TEXT,
    ADD COLUMN parent_id           TEXT REFERENCES documents(id) ON DELETE SET NULL,
    ADD COLUMN acceptance_criteria TEXT NOT NULL DEFAULT '',
    ADD COLUMN summary             TEXT NOT NULL DEFAULT '',
    ADD COLUMN summary_updated_at  TIMESTAMPTZ;

-- Type-aware uniqueness: only type='document' rows participate in the
-- pre-existing (scope, [workspace], [project], name) uniqueness.
-- Stories are addressed by id; duplicate titles are allowed.
DROP INDEX documents_system_unique;
DROP INDEX documents_workspace_unique;
DROP INDEX documents_project_unique;

CREATE UNIQUE INDEX documents_system_unique
    ON documents (name) WHERE scope = 'system' AND type = 'document';
CREATE UNIQUE INDEX documents_workspace_unique
    ON documents (workspace_id, name) WHERE scope = 'workspace' AND type = 'document';
CREATE UNIQUE INDEX documents_project_unique
    ON documents (workspace_id, project_id, name) WHERE scope = 'project' AND type = 'document';

-- Indices for the unified filter surface (document_list).
CREATE INDEX documents_project_type_status ON documents (project_id, type, status);
CREATE INDEX documents_type                ON documents (type);
CREATE INDEX documents_tags_gin            ON documents USING GIN (tags);
CREATE INDEX documents_parent              ON documents (parent_id) WHERE parent_id IS NOT NULL;

------------------------------------------------------------------------
-- 2. Move stories into documents.
--
-- Stories are project-scoped by construction. workspace_id is sourced
-- from projects.workspace_id via JOIN. Story ids (sty_<hex>) are
-- preserved so evidence.story_id rows continue to resolve without an
-- FK repoint.
------------------------------------------------------------------------

INSERT INTO documents (
    id, scope, workspace_id, project_id, name, latest_version,
    type, tags, status, priority, category, parent_id,
    acceptance_criteria, summary, summary_updated_at,
    created_at, updated_at
)
SELECT
    s.id, 'project', p.workspace_id, s.project_id, s.title, 1,
    'story', s.tags, s.status, s.priority, s.category, s.parent_id,
    s.acceptance_criteria, s.summary, s.summary_updated_at,
    s.created_at, s.updated_at
FROM stories s
JOIN projects p ON p.id = s.project_id;

-- One initial document_versions row per migrated story carries the
-- body. Subsequent story_update calls append v2, v3, … like any other
-- document.
INSERT INTO document_versions (
    document_id, version, body, status, created_at, created_by
)
SELECT s.id, 1, s.body, 'active', s.created_at, 'migrate:0017'
FROM stories s;

------------------------------------------------------------------------
-- 3. Drop the now-empty stories table.
--
-- evidence.story_id (the ledger primitive) is a TEXT column with no
-- FK, so ledger entries continue to resolve against documents.id by
-- value. The self-FK stories.parent_id → stories(id) is gone with the
-- table; the equivalent documents.parent_id self-FK was set above.
------------------------------------------------------------------------

DROP TABLE stories;

COMMIT;
