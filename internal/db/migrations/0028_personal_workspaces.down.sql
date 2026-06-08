-- 0028_personal_workspaces.down.sql — reverse of the up migration.
--
-- Restore the shared default by re-flagging the global admin's personal
-- workspace (the repurposed former default), then drop the personal
-- machinery. Personal workspaces minted for other users are left in place
-- (non-destructive, documented — like 0027's partial down).

WITH adminws AS (
    SELECT w.id
    FROM workspaces w
    JOIN users u ON u.id = w.owner_user_id
    WHERE w.is_personal = TRUE AND u.role = 'admin'
    ORDER BY u.created_at ASC, u.id ASC
    LIMIT 1
)
UPDATE workspaces
SET is_default = TRUE, owner_user_id = NULL, is_personal = FALSE, updated_at = now()
WHERE id IN (SELECT id FROM adminws);

DROP INDEX IF EXISTS workspaces_one_personal_per_owner;

ALTER TABLE workspaces DROP COLUMN IF EXISTS is_personal;
