-- 0031_documents_headline.up.sql — caveman one-line headline column
-- (epic:always-context, order:1, sty_285292a6).
--
-- headline holds a terse, drift-free one-liner describing the document or
-- principle, used by `satellites document index` so the dispatch listing stays
-- context-cheap (the index never loads bodies). Authored in frontmatter on
-- upload and stored on the row; a later story generates it when absent. Mirrors
-- the existing `summary` column (a derived text field persisted on the row so
-- reads stay free). Absent → empty string, never null.

ALTER TABLE documents
    ADD COLUMN headline TEXT NOT NULL DEFAULT '';
