-- 0003_password.down.sql — reverse of 0003_password.up.sql

ALTER TABLE users DROP COLUMN IF EXISTS password_hash;
