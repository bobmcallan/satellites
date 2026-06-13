-- 0038_document_chunks.up.sql — workspace corpus embeddings (sty_7f4f7e11).
-- One row per text chunk of a workspace document: the chunk content + its
-- Gemini embedding (stored as a double precision array so cosine similarity is
-- computed brute-force in Go — no pgvector dependency; ANN is deferred until a
-- corpus needs it). doc_version records the document's latest_version at embed
-- time so the reconcile worker can detect a stale chunk set. FK CASCADE cleans
-- chunks on a hard document delete; soft-deletes (tombstones) are pruned by the
-- reconcile loop.

CREATE TABLE document_chunks (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    document_id  TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    chunk_index  INTEGER NOT NULL,
    content      TEXT NOT NULL,
    token_count  INTEGER NOT NULL DEFAULT 0,
    embedding    DOUBLE PRECISION[] NOT NULL,
    dim          INTEGER NOT NULL,
    doc_version  INTEGER NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX document_chunks_workspace ON document_chunks (workspace_id);
CREATE INDEX document_chunks_document ON document_chunks (document_id);
