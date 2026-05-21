-- 0005_workspaces.up.sql — multi-tenant root: every project (and
-- transitively, every story/tool/review/evidence row) belongs to
-- exactly one workspace.
--
-- PR 1 of the workspaces/projects/stories slice: workspaces table only.
-- Membership (workspace_members) and the projects.workspace_id FK
-- arrive in subsequent migrations.

CREATE TABLE workspaces (
    id              TEXT PRIMARY KEY,                                    -- wksp_<8hex>
    name            TEXT NOT NULL,
    owner_user_id   TEXT REFERENCES users(id) ON DELETE SET NULL,
    status          TEXT NOT NULL DEFAULT 'active',
    is_default      BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- At most one default workspace exists at any time. The boot-time seed
-- writes a single NULL-owner default; per-owner defaults arrive when
-- membership lands and this index is re-keyed to (owner_user_id).
CREATE UNIQUE INDEX workspaces_only_one_default
    ON workspaces (is_default)
    WHERE is_default = TRUE;
