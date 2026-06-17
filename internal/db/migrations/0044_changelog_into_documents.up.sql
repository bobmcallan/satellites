-- 0044_changelog_into_documents.up.sql — collapse the dedicated `changelog`
-- table + changelog_* verbs into generic documents of type 'changelog'
-- (epic:satellites-backbone, Workstream C / C-min). Changelog entries are
-- release notes: the typed columns (service / version_from / version_to /
-- effective_date) become tags on a system-scope document, and the content
-- becomes the document's first version body. The /changelog portal page reads
-- documents instead of the table. Reversible — the down migration recreates the
-- table and copies the rows back out of documents.

BEGIN;

-- 1. Admit 'changelog' to the documents type check (the 0018/0022 pattern).
ALTER TABLE documents DROP CONSTRAINT documents_type_check;
ALTER TABLE documents ADD CONSTRAINT documents_type_check
    CHECK (type IN ('document','story','task','skill','changelog'));

-- 2. One type:changelog document per changelog row. scope=system (workspace_id
--    / project_id / user_id stay NULL per the scope-coherence check); id + name
--    reuse the stable cl_<hex> id so the row is uniquely addressable. The typed
--    fields ride as tags.
INSERT INTO documents (id, scope, name, latest_version, type, tags, status, created_at, updated_at)
SELECT
    c.id, 'system', c.id, 1, 'changelog',
    ARRAY['changelog',
          'service:'      || c.service,
          'version_from:' || c.version_from,
          'version_to:'   || c.version_to]
      || CASE WHEN c.effective_date IS NOT NULL
              THEN ARRAY['effective_date:' || to_char(c.effective_date AT TIME ZONE 'UTC', 'YYYY-MM-DD')]
              ELSE ARRAY[]::text[] END,
    'active', c.created_at, c.updated_at
FROM changelog c;

-- 3. The content becomes the document's first version body.
INSERT INTO document_versions (document_id, version, body, status, created_at, created_by)
SELECT c.id, 1, c.content, 'active', c.created_at, COALESCE(c.created_by, '')
FROM changelog c;

-- 4. Retire the dedicated table + its indexes.
DROP TABLE changelog;

COMMIT;
