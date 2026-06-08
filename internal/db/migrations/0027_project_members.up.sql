-- 0027_project_members.up.sql — epic:user-admin foundation (sty_0ce67ed2).
--
-- (a) The per-project membership layer. Mirrors workspace_members (0008):
--     each row binds (project, user) at a project role (read|write|admin),
--     PRIMARY KEY (project_id, user_id) enforces one membership per pair,
--     CASCADE on both FKs cleans up on project_delete / user_delete.
-- (b) Realign workspace roles to the GitHub model owner|admin|member:
--     fold the retired reviewer/viewer rows to member, then guarantee
--     exactly one owner per workspace (the declared creator, else the
--     oldest admin/member). Idempotent — re-running is a no-op once the
--     role set is already clean.

CREATE TABLE IF NOT EXISTS project_members (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    role       TEXT NOT NULL,
    added_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    added_by   TEXT,
    PRIMARY KEY (project_id, user_id)
);

CREATE INDEX IF NOT EXISTS project_members_user ON project_members (user_id);

-- Fold retired workspace roles to member.
UPDATE workspace_members SET role = 'member' WHERE role IN ('reviewer', 'viewer');

-- Promote exactly one owner per workspace: prefer the workspace's declared
-- owner_user_id, then the lowest-ranked existing role (owner>admin>member),
-- then the oldest membership. Demote any other owner rows back to admin so
-- the one-owner invariant holds even if data already had multiple owners.
WITH ranked AS (
    SELECT wm.workspace_id, wm.user_id,
           ROW_NUMBER() OVER (
               PARTITION BY wm.workspace_id
               ORDER BY
                   COALESCE((wm.user_id = w.owner_user_id), false) DESC,
                   CASE wm.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END ASC,
                   wm.added_at ASC,
                   wm.user_id ASC
           ) AS rn
    FROM workspace_members wm
    JOIN workspaces w ON w.id = wm.workspace_id
)
UPDATE workspace_members m
SET role = 'owner'
FROM ranked r
WHERE m.workspace_id = r.workspace_id
  AND m.user_id = r.user_id
  AND r.rn = 1
  AND m.role <> 'owner';

WITH ranked AS (
    SELECT wm.workspace_id, wm.user_id,
           ROW_NUMBER() OVER (
               PARTITION BY wm.workspace_id
               ORDER BY
                   COALESCE((wm.user_id = w.owner_user_id), false) DESC,
                   CASE wm.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END ASC,
                   wm.added_at ASC,
                   wm.user_id ASC
           ) AS rn
    FROM workspace_members wm
    JOIN workspaces w ON w.id = wm.workspace_id
)
UPDATE workspace_members m
SET role = 'admin'
FROM ranked r
WHERE m.workspace_id = r.workspace_id
  AND m.user_id = r.user_id
  AND r.rn <> 1
  AND m.role = 'owner';
