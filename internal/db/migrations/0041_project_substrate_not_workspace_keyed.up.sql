-- 0041_project_substrate_not_workspace_keyed.up.sql (sty_c6de961e)
-- Project-scoped substrate is keyed by project, not workspace: a home-workspace
-- move must not orphan a project's skills/principles/documents. Dedup rows a
-- prior move + emergency re-home left duplicated under different workspaces, null
-- the now-vestigial workspace_id, and re-key the unique index off it.

-- 0. Relax scope coherence: project-scoped substrate is keyed by project, so a
--    project doc/skill row may carry workspace_id NULL. (Project stories keep
--    their workspace_id; the branch now only requires project_id.)
ALTER TABLE documents DROP CONSTRAINT documents_scope_coherence;
ALTER TABLE documents ADD CONSTRAINT documents_scope_coherence CHECK (
    (scope = 'system'    AND workspace_id IS NULL     AND project_id IS NULL     AND user_id IS NULL)     OR
    (scope = 'workspace' AND workspace_id IS NOT NULL AND project_id IS NULL     AND user_id IS NULL)     OR
    (scope = 'project'                                AND project_id IS NOT NULL AND user_id IS NULL)     OR
    (scope = 'user'      AND workspace_id IS NULL     AND project_id IS NULL     AND user_id IS NOT NULL) OR
    (scope = 'library'   AND workspace_id IS NULL     AND project_id IS NOT NULL AND user_id IS NULL)
);

-- 1. Dedup: per (project_id, name) project doc/skill group keep one row — the one
--    under the project's CURRENT home if present, else the most recently updated.
DELETE FROM document_versions WHERE document_id IN (
    SELECT id FROM (
        SELECT d.id,
               row_number() OVER (
                   PARTITION BY d.project_id, d.name
                   ORDER BY (d.workspace_id IS NOT DISTINCT FROM p.workspace_id) DESC,
                            d.updated_at DESC, d.id
               ) AS rn
        FROM documents d LEFT JOIN projects p ON p.id = d.project_id
        WHERE d.scope = 'project' AND d.type IN ('document','skill')
    ) ranked WHERE rn > 1
);
DELETE FROM documents WHERE id IN (
    SELECT id FROM (
        SELECT d.id,
               row_number() OVER (
                   PARTITION BY d.project_id, d.name
                   ORDER BY (d.workspace_id IS NOT DISTINCT FROM p.workspace_id) DESC,
                            d.updated_at DESC, d.id
               ) AS rn
        FROM documents d LEFT JOIN projects p ON p.id = d.project_id
        WHERE d.scope = 'project' AND d.type IN ('document','skill')
    ) ranked WHERE rn > 1
);

-- 2. workspace_id is now vestigial for project substrate — null it so resolution
--    is purely (project_id, name). Stories (type='story') are untouched.
UPDATE documents SET workspace_id = NULL
 WHERE scope = 'project' AND type IN ('document','skill');

-- 3. Re-key uniqueness off workspace_id.
DROP INDEX IF EXISTS documents_project_unique;
CREATE UNIQUE INDEX documents_project_unique
    ON documents (project_id, name) WHERE scope = 'project' AND type IN ('document','skill');
