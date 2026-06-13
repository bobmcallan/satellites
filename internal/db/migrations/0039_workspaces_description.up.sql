-- 0039_workspaces_description.up.sql — patchable workspace description (sty_7d7f3037).
-- workspace_upsert (epic:workspace-admin) patches a freeform description rendered
-- on the workspace page. NOT NULL DEFAULT '' so existing rows and the create path
-- (which sets it later, if at all) carry an empty string rather than NULL.

ALTER TABLE workspaces ADD COLUMN description TEXT NOT NULL DEFAULT '';
