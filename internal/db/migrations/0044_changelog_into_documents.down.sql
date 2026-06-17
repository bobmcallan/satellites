-- 0044_changelog_into_documents.down.sql — reverse: recreate the changelog
-- table (the 0020 shape), copy the type:changelog documents back into it, delete
-- those documents, and re-narrow the documents type check. Best-effort
-- round-trip — the typed fields are recovered from the document tags.

BEGIN;

CREATE TABLE changelog (
    id              TEXT PRIMARY KEY,
    service         TEXT NOT NULL,
    version_from    TEXT NOT NULL DEFAULT '',
    version_to      TEXT NOT NULL,
    content         TEXT NOT NULL,
    created_by      TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    effective_date  TIMESTAMPTZ
);
CREATE INDEX changelog_service ON changelog (service);
CREATE INDEX changelog_listing ON changelog (effective_date DESC NULLS LAST, created_at DESC, id);

INSERT INTO changelog (id, service, version_from, version_to, content, created_by, created_at, updated_at, effective_date)
SELECT
    d.id,
    COALESCE((SELECT substring(t from 'service:(.*)')      FROM unnest(d.tags) t WHERE t LIKE 'service:%'      LIMIT 1), ''),
    COALESCE((SELECT substring(t from 'version_from:(.*)') FROM unnest(d.tags) t WHERE t LIKE 'version_from:%' LIMIT 1), ''),
    COALESCE((SELECT substring(t from 'version_to:(.*)')   FROM unnest(d.tags) t WHERE t LIKE 'version_to:%'   LIMIT 1), ''),
    COALESCE(v.body, ''),
    NULLIF(v.created_by, ''),
    d.created_at, d.updated_at,
    (SELECT to_timestamp(substring(t from 'effective_date:(.*)'), 'YYYY-MM-DD')
       FROM unnest(d.tags) t WHERE t LIKE 'effective_date:%' LIMIT 1)
FROM documents d
LEFT JOIN document_versions v ON v.document_id = d.id AND v.version = d.latest_version
WHERE d.type = 'changelog';

DELETE FROM documents WHERE type = 'changelog';

ALTER TABLE documents DROP CONSTRAINT documents_type_check;
ALTER TABLE documents ADD CONSTRAINT documents_type_check
    CHECK (type IN ('document','story','task','skill'));

COMMIT;
