-- 0008_workspace_members.up.sql — multi-tenant membership rows.
-- Each row binds (workspace, user) at a role. The PRIMARY KEY on
-- (workspace_id, user_id) enforces one membership per pair; CASCADE
-- on both FKs lets a workspace_delete or user_delete clean up
-- transitively.

CREATE TABLE workspace_members (
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    user_id      TEXT NOT NULL REFERENCES users(id)      ON DELETE CASCADE,
    role         TEXT NOT NULL,
    added_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    added_by     TEXT,
    PRIMARY KEY (workspace_id, user_id)
);

CREATE INDEX workspace_members_user ON workspace_members (user_id);
