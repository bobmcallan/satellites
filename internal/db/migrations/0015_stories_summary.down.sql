-- 0015_stories_summary.down.sql — drop summary columns.

ALTER TABLE stories
    DROP COLUMN IF EXISTS summary_updated_at,
    DROP COLUMN IF EXISTS summary;
