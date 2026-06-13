-- 0037_workspace_mounts.up.sql — readonly repo mounts (sty_1ede3853).
-- A writable repo's home is project.workspace_id (exactly one home). A readonly
-- mount lets that repo appear in OTHER workspaces as reference corpus, granting
-- read (never write) to the mounting workspace's members. PRIMARY KEY
-- (workspace_id, project_id) enforces one mount per pair; CASCADE on both FKs
-- cleans up on workspace_delete or project_delete. The project_id index is the
-- reverse lookup the authz resolver uses ("which workspaces mount this repo?").

CREATE TABLE workspace_mounts (
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    project_id   TEXT NOT NULL REFERENCES projects(id)   ON DELETE CASCADE,
    access       TEXT NOT NULL DEFAULT 'read',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by   TEXT,
    PRIMARY KEY (workspace_id, project_id)
);

CREATE INDEX workspace_mounts_project ON workspace_mounts (project_id);
