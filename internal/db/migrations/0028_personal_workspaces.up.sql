-- 0028_personal_workspaces.up.sql — epic:user-admin (sty_3a54bf42).
--
-- Move from a single shared boot-seeded default to a personal workspace
-- per user, and retire the default.
--   (a) is_personal column + one-personal-per-owner unique index.
--   (b) Repurpose the shared default IN PLACE as the global admin's
--       personal workspace (no project's workspace_id changes).
--   (c) Backfill a personal workspace for every other user.
-- Idempotent — re-running is a no-op once the shape is established.

ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS is_personal BOOLEAN NOT NULL DEFAULT FALSE;

CREATE UNIQUE INDEX IF NOT EXISTS workspaces_one_personal_per_owner
    ON workspaces (owner_user_id)
    WHERE is_personal = TRUE;

-- (b) Repurpose the default workspace as the oldest global admin's personal
-- workspace: assign ownership, flag personal, clear the default flag, and
-- guarantee that admin holds an owner membership. Guarded by the JOIN — if
-- there is no global admin or no default workspace, nothing changes.
WITH admin AS (
    SELECT id FROM users WHERE role = 'admin' ORDER BY created_at ASC, id ASC LIMIT 1
),
def AS (
    SELECT id FROM workspaces WHERE is_default = TRUE LIMIT 1
)
UPDATE workspaces w
SET owner_user_id = (SELECT id FROM admin),
    is_personal   = TRUE,
    is_default    = FALSE,
    updated_at    = now()
WHERE w.id = (SELECT id FROM def)
  AND (SELECT id FROM admin) IS NOT NULL
  AND NOT EXISTS (
        SELECT 1 FROM workspaces p
        WHERE p.owner_user_id = (SELECT id FROM admin) AND p.is_personal = TRUE
  );

-- Ensure every personal workspace's owner holds an owner membership row.
INSERT INTO workspace_members (workspace_id, user_id, role, added_at, added_by)
SELECT w.id, w.owner_user_id, 'owner', now(), w.owner_user_id
FROM workspaces w
WHERE w.is_personal = TRUE AND w.owner_user_id IS NOT NULL
ON CONFLICT (workspace_id, user_id) DO UPDATE SET role = 'owner';

-- (c) Backfill a personal workspace for every user who lacks one. The id is
-- derived deterministically from the user id so a re-run collides on the
-- primary key (ON CONFLICT DO NOTHING) rather than minting duplicates.
WITH need AS (
    SELECT u.id AS user_id,
           COALESCE(NULLIF(u.display_name, ''), u.email) AS nm
    FROM users u
    WHERE NOT EXISTS (
        SELECT 1 FROM workspaces w
        WHERE w.owner_user_id = u.id AND w.is_personal = TRUE
    )
),
created AS (
    INSERT INTO workspaces (id, name, owner_user_id, status, is_default, is_personal, created_at, updated_at)
    SELECT 'wksp_' || substr(md5('personal:' || user_id), 1, 8), nm, user_id, 'active', FALSE, TRUE, now(), now()
    FROM need
    ON CONFLICT (id) DO NOTHING
    RETURNING id, owner_user_id
)
INSERT INTO workspace_members (workspace_id, user_id, role, added_at, added_by)
SELECT id, owner_user_id, 'owner', now(), owner_user_id FROM created
ON CONFLICT (workspace_id, user_id) DO UPDATE SET role = 'owner';
