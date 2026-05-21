-- 0003_password.up.sql — bcrypt password hashes for UI login.
-- API key auth (Bearer) remains primary for agent/MCP traffic; passwords
-- are for human login through the browser UI.

ALTER TABLE users ADD COLUMN password_hash TEXT NOT NULL DEFAULT '';
