DROP INDEX IF EXISTS stories_parent;
ALTER TABLE stories DROP COLUMN IF EXISTS parent_id;
