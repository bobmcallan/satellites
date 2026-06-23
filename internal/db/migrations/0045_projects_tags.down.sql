-- 0045_projects_tags.down.sql — drop the projects.tags column.
-- The legacy `type` column was left intact by the up migration, so classification
-- set via the type: tag after this migration is lost on rollback (expected).

ALTER TABLE projects DROP COLUMN tags;
