-- 0017_unify_stories_into_documents.down.sql — immediate-rollback path.
--
-- Recreates the stories table and copies the LATEST body from
-- document_versions back into stories.body. Version history is lost on
-- rollback (the stories table never had a versions column); operators
-- who care about that should restore from backup instead.

BEGIN;

CREATE TABLE stories (
    id                  TEXT PRIMARY KEY,
    project_id          TEXT NOT NULL,
    title               TEXT NOT NULL,
    body                TEXT NOT NULL DEFAULT '',
    acceptance_criteria TEXT NOT NULL DEFAULT '',
    status              TEXT NOT NULL DEFAULT 'backlog',
    priority            TEXT NOT NULL DEFAULT 'medium',
    category            TEXT NOT NULL DEFAULT 'feature',
    tags                TEXT[] NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    parent_id           TEXT REFERENCES stories(id) ON DELETE SET NULL,
    summary             TEXT NOT NULL DEFAULT '',
    summary_updated_at  TIMESTAMPTZ
);

CREATE INDEX stories_project_status ON stories (project_id, status);
CREATE INDEX stories_tags_gin       ON stories USING GIN (tags);
CREATE INDEX stories_parent         ON stories (parent_id) WHERE parent_id IS NOT NULL;

INSERT INTO stories (
    id, project_id, title, body, acceptance_criteria,
    status, priority, category, tags,
    created_at, updated_at, parent_id, summary, summary_updated_at
)
SELECT
    d.id, d.project_id, d.name,
    COALESCE(v.body, ''),
    d.acceptance_criteria,
    d.status,
    COALESCE(d.priority, 'medium'),
    COALESCE(d.category, 'feature'),
    d.tags,
    d.created_at, d.updated_at, d.parent_id, d.summary, d.summary_updated_at
FROM documents d
LEFT JOIN LATERAL (
    SELECT body FROM document_versions
    WHERE document_id = d.id AND status = 'active'
    ORDER BY version DESC LIMIT 1
) v ON TRUE
WHERE d.type = 'story';

-- Remove the story rows + versions from the unified table.
DELETE FROM document_versions
 WHERE document_id IN (SELECT id FROM documents WHERE type = 'story');
DELETE FROM documents WHERE type = 'story';

-- Reverse the schema additions.
DROP INDEX IF EXISTS documents_parent;
DROP INDEX IF EXISTS documents_tags_gin;
DROP INDEX IF EXISTS documents_type;
DROP INDEX IF EXISTS documents_project_type_status;

DROP INDEX IF EXISTS documents_system_unique;
DROP INDEX IF EXISTS documents_workspace_unique;
DROP INDEX IF EXISTS documents_project_unique;

CREATE UNIQUE INDEX documents_system_unique
    ON documents (name) WHERE scope = 'system';
CREATE UNIQUE INDEX documents_workspace_unique
    ON documents (workspace_id, name) WHERE scope = 'workspace';
CREATE UNIQUE INDEX documents_project_unique
    ON documents (workspace_id, project_id, name) WHERE scope = 'project';

ALTER TABLE documents
    DROP COLUMN summary_updated_at,
    DROP COLUMN summary,
    DROP COLUMN acceptance_criteria,
    DROP COLUMN parent_id,
    DROP COLUMN category,
    DROP COLUMN priority,
    DROP COLUMN status,
    DROP COLUMN tags,
    DROP COLUMN type;

COMMIT;
