-- 0006_projects.up.sql — project rows bound to a workspace, with the
-- canonicalised git remote stored directly on the row (no separate
-- repo table at this layer).
--
-- Also adds the FK from stories.project_id (declared NOT NULL in 0001
-- but unbacked until now). Existing stories rows must reference a
-- valid project, but at this point in v5's lifecycle the stories table
-- is empty — no project_create existed before, so no orphans.

CREATE TABLE projects (
    id                  TEXT PRIMARY KEY,                                        -- proj_<8hex>
    workspace_id        TEXT NOT NULL REFERENCES workspaces(id) ON DELETE RESTRICT,
    name                TEXT NOT NULL,
    description         TEXT NOT NULL DEFAULT '',
    git_url_canonical   TEXT NOT NULL DEFAULT '',
    owner_user_id       TEXT REFERENCES users(id) ON DELETE SET NULL,
    status              TEXT NOT NULL DEFAULT 'active',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX projects_workspace ON projects (workspace_id);

-- One canonical git URL per workspace. The partial-index condition lets
-- multiple rows share the empty default ("git-less project" is a valid
-- shape for ideas and exploratory work).
CREATE UNIQUE INDEX projects_workspace_git_unique
    ON projects (workspace_id, git_url_canonical)
    WHERE git_url_canonical <> '';

ALTER TABLE stories
    ADD CONSTRAINT stories_project_fk
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE;
