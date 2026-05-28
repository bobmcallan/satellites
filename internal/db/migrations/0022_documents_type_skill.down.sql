-- 0022_documents_type_skill.down.sql — drop 'skill' from the type
-- discriminator and restore the document-only namespace on the unique
-- indices. Any rows currently typed as 'skill' would violate the
-- restored CHECK; the down path deletes those rows up-front. Operators
-- with skill rows they care about should back up before rolling back.

BEGIN;

DELETE FROM document_versions WHERE document_id IN (SELECT id FROM documents WHERE type = 'skill');
DELETE FROM documents WHERE type = 'skill';

DROP INDEX IF EXISTS documents_system_unique;
DROP INDEX IF EXISTS documents_workspace_unique;
DROP INDEX IF EXISTS documents_project_unique;

CREATE UNIQUE INDEX documents_system_unique
    ON documents (name) WHERE scope = 'system' AND type = 'document';
CREATE UNIQUE INDEX documents_workspace_unique
    ON documents (workspace_id, name) WHERE scope = 'workspace' AND type = 'document';
CREATE UNIQUE INDEX documents_project_unique
    ON documents (workspace_id, project_id, name) WHERE scope = 'project' AND type = 'document';

ALTER TABLE documents DROP CONSTRAINT documents_type_check;
ALTER TABLE documents
    ADD CONSTRAINT documents_type_check CHECK (type IN ('document','story','task'));

COMMIT;
