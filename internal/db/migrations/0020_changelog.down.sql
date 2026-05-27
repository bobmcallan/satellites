BEGIN;

DROP INDEX IF EXISTS changelog_listing;
DROP INDEX IF EXISTS changelog_service;
DROP TABLE IF EXISTS changelog;

COMMIT;
