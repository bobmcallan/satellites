-- 0034_invitation_links.down.sql
DROP INDEX IF EXISTS invitations_token;
ALTER TABLE invitations DROP COLUMN IF EXISTS accepted_by;
ALTER TABLE invitations DROP COLUMN IF EXISTS expires_at;
ALTER TABLE invitations DROP COLUMN IF EXISTS token;
-- email is left nullable — re-adding NOT NULL would fail on existing link rows.
