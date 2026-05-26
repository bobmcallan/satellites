-- 0018_documents_type_task.down.sql — drop 'task' from the type
-- discriminator. Any rows currently typed as 'task' would violate the
-- restored CHECK; the down path deletes those rows up-front. Operators
-- with task rows they care about should back up before rolling back.

BEGIN;

DELETE FROM document_versions WHERE document_id IN (SELECT id FROM documents WHERE type = 'task');
DELETE FROM documents WHERE type = 'task';

ALTER TABLE documents DROP CONSTRAINT documents_type_check;
ALTER TABLE documents
    ADD CONSTRAINT documents_type_check CHECK (type IN ('document','story'));

COMMIT;
