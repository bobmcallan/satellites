-- 0011_variables.up.sql — variables-as-substrate.
--
-- variables hold operator-set name/value pairs at workspace or project
-- scope. Same scope-coherence rules as documents:
--   workspace scope → workspace_id set, project_id NULL
--   project   scope → workspace_id + project_id set
--
-- The system scope is intentionally absent here: the six system
-- variables (version, os, arch, server_url, state, current_version)
-- are computed per-request by the templating layer (story 5) and never
-- materialised as table rows. The store's CHECK constraint refuses
-- scope='system' so a stray insert can't quietly create an unsupported
-- shadow row.

CREATE TABLE variables (
    id              TEXT PRIMARY KEY,                                          -- var_<8hex>
    scope           TEXT NOT NULL CHECK (scope IN ('workspace','project')),
    workspace_id    TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    project_id      TEXT REFERENCES projects(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    value           TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT variables_scope_coherence CHECK (
        (scope = 'workspace' AND project_id IS NULL) OR
        (scope = 'project'   AND project_id IS NOT NULL)
    )
);

CREATE INDEX variables_locator ON variables (scope, workspace_id, project_id, name);

CREATE UNIQUE INDEX variables_workspace_unique
    ON variables (workspace_id, name) WHERE scope = 'workspace';
CREATE UNIQUE INDEX variables_project_unique
    ON variables (workspace_id, project_id, name) WHERE scope = 'project';
