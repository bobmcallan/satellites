-- 0045_projects_tags.up.sql — KV tags on projects (sty_87379afa, epic:phases-task-outputs).
-- Mirror the document model: a project carries free-form []string tags, with
-- classification (type:) and phase (phase:) as single-valued KV tags read via
-- internal/kvtag. The legacy opaque `type` column (0040) is backfilled into a
-- `type:<value>` tag and kept (vestigial) for rollback — classification now
-- lives in the tag.

ALTER TABLE projects ADD COLUMN tags TEXT[] NOT NULL DEFAULT '{}';

-- Backfill: carry any existing classification into a type: tag.
UPDATE projects SET tags = ARRAY['type:' || type] WHERE type IS NOT NULL AND type <> '';
