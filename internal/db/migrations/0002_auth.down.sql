-- 0002_auth.down.sql — reverse of 0002_auth.up.sql

DROP INDEX IF EXISTS api_keys_project_agent;
DROP INDEX IF EXISTS api_keys_user;
DROP TABLE IF EXISTS api_keys;
DROP INDEX IF EXISTS users_oauth_id;
DROP TABLE IF EXISTS users;
