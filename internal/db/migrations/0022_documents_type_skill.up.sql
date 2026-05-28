-- 0022_documents_type_skill.up.sql — add 'skill' as a fourth
-- documents.type discriminator alongside 'document', 'story', 'task'.
-- Skills are documents with type='skill'; they share the (scope,
-- workspace_id, project_id, name) namespace with type='document' rows
-- so document_get can resolve by (scope, name) without a type
-- predicate. The unique indices that previously constrained only
-- type='document' now constrain document+skill together.
--
-- A skill is a documents row with:
--   - type        = 'skill'
--   - scope       = any of system / workspace / project
--   - body        = the SKILL.md content the agent materialises into
--                   .claude/skills/<name>/SKILL.md
--
-- No skill-specific columns are added in this migration — the existing
-- documents column set (name, tags, body via document_versions) covers
-- the substrate shape. Versioning and the version chain come for free
-- from document_versions.

BEGIN;

-- Extend the type CHECK constraint to admit 'skill'. Postgres requires
-- the old constraint to go before the new one can take its place under
-- the same column.
ALTER TABLE documents DROP CONSTRAINT documents_type_check;
ALTER TABLE documents
    ADD CONSTRAINT documents_type_check CHECK (type IN ('document','story','task','skill'));

-- Generalise the (scope, …, name) unique indices so skills and
-- documents share a single namespace. A document named "foo" and a
-- skill named "foo" at the same scope cannot coexist — the lookup
-- (scope, name) → row stays unambiguous, which the document_get verb
-- relies on.
DROP INDEX IF EXISTS documents_system_unique;
DROP INDEX IF EXISTS documents_workspace_unique;
DROP INDEX IF EXISTS documents_project_unique;

CREATE UNIQUE INDEX documents_system_unique
    ON documents (name) WHERE scope = 'system' AND type IN ('document','skill');
CREATE UNIQUE INDEX documents_workspace_unique
    ON documents (workspace_id, name) WHERE scope = 'workspace' AND type IN ('document','skill');
CREATE UNIQUE INDEX documents_project_unique
    ON documents (workspace_id, project_id, name) WHERE scope = 'project' AND type IN ('document','skill');

COMMIT;
