-- 0020_changelog.up.sql — release-note changelog entries per service.
--
-- Operator-facing surface for "what changed in vX.Y.Z" notes that the
-- public /changelog page reads. Ported from V3
-- (internal/models/changelog.go) onto Postgres. Entries are written
-- through the changelog_add verb; created_by is server-derived from
-- the authed identity and never trusted from the client.
--
-- Listing order is (effective_date DESC NULLS LAST, created_at DESC,
-- id) so backfill rows without effective_date still surface, sorted by
-- when they were entered. Index covers the listing path; service
-- filter is the dominant access pattern.

BEGIN;

CREATE TABLE changelog (
    id              TEXT PRIMARY KEY,                          -- cl_<8hex>
    service         TEXT NOT NULL,
    version_from    TEXT NOT NULL DEFAULT '',
    version_to      TEXT NOT NULL,
    content         TEXT NOT NULL,                             -- markdown
    created_by      TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    effective_date  TIMESTAMPTZ
);

CREATE INDEX changelog_service ON changelog (service);
CREATE INDEX changelog_listing ON changelog (effective_date DESC NULLS LAST, created_at DESC, id);

COMMIT;
