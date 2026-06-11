-- 0032_blobs.up.sql — binary ingestion (sty_59652a7d).
--
-- Document-collection projects accept binary uploads (PDFs, decks, images)
-- that cannot ride an MCP tool call. The blob lands here in Postgres bytea
-- (satellites-native storage; an object store is a later swap behind the same
-- store seam). The extractor (sty_52c2393f) reads a blob and materialises a
-- text-bodied document referencing blobs.id. Additive, backward-compatible.
CREATE TABLE IF NOT EXISTS blobs (
    id            TEXT PRIMARY KEY,
    workspace_id  TEXT NOT NULL,
    project_id    TEXT NOT NULL,
    filename      TEXT NOT NULL DEFAULT '',
    content_type  TEXT NOT NULL DEFAULT '',
    size_bytes    BIGINT NOT NULL,
    sha256        TEXT NOT NULL,
    content       BYTEA NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL,
    created_by    TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS blobs_project_idx ON blobs (project_id, created_at DESC);
