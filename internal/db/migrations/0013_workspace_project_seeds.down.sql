-- 0013_workspace_project_seeds.down.sql — drop the seed columns.

ALTER TABLE projects   DROP COLUMN IF EXISTS seed_updated_at, DROP COLUMN IF EXISTS seed_md;
ALTER TABLE workspaces DROP COLUMN IF EXISTS seed_updated_at, DROP COLUMN IF EXISTS seed_md;
