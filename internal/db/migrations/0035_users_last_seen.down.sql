-- 0035_users_last_seen.down.sql
ALTER TABLE users DROP COLUMN IF EXISTS last_seen_at;
