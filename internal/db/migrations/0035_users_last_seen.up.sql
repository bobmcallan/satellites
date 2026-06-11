-- 0035_users_last_seen.up.sql — epic:project-invites (sty_e34daf4d).
--
-- Cheapest source for a member's "last accessed": one nullable timestamp on
-- users, written throttled by Store.TouchLastSeen on authenticated access
-- (Bearer middleware + portal session). NULL means never seen. No new event
-- stream or per-session table — sessions are stateless cookies.
ALTER TABLE users ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMPTZ;
