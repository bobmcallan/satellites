-- 0001_init.down.sql — reverse of 0001_init.up.sql

DROP TRIGGER IF EXISTS evidence_no_delete ON evidence;
DROP TRIGGER IF EXISTS evidence_no_update ON evidence;
DROP FUNCTION IF EXISTS evidence_append_only();

DROP TABLE IF EXISTS evidence;
DROP TABLE IF EXISTS reviews;
DROP TABLE IF EXISTS tools;
DROP TABLE IF EXISTS stories;

-- pgcrypto extension is left in place (shared / harmless).
