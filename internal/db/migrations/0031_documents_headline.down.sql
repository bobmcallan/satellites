-- 0031_documents_headline.down.sql — drop the headline column.

ALTER TABLE documents
    DROP COLUMN headline;
