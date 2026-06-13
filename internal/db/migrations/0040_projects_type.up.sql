-- 0040_projects_type.up.sql — admin-settable freeform project type (sty_e5dbde6d).
-- Opaque text (no enum). NOT NULL DEFAULT '' so existing rows and the create path
-- carry an empty string; project_update patches it (field-merge). A downstream
-- consumer may later dispatch on it.

ALTER TABLE projects ADD COLUMN type TEXT NOT NULL DEFAULT '';
