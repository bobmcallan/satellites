-- 0007_story_parent.up.sql — story self-relationship for v4 parity.
-- A story may declare another story as its parent (used for epic →
-- story decomposition). Self-FK with SET NULL on parent delete: a
-- deleted parent leaves the child orphaned (no implicit cascade).

ALTER TABLE stories
    ADD COLUMN parent_id TEXT REFERENCES stories(id) ON DELETE SET NULL;

CREATE INDEX stories_parent ON stories (parent_id) WHERE parent_id IS NOT NULL;
