-- 0029_invitations.up.sql — epic:user-admin (sty_0e88352a).
--
-- Pending email invitations. An admin records an invite by email; it is
-- claimed (membership created) when that verified email logs in, or
-- immediately if the user already exists. scope is workspace|project; the
-- target is workspace_id (workspace scope) or project_id (project scope).
-- role is validated app-side against the scope's role set.

CREATE TABLE invitations (
    id           TEXT PRIMARY KEY,
    email        TEXT NOT NULL,
    scope        TEXT NOT NULL,
    workspace_id TEXT REFERENCES workspaces(id) ON DELETE CASCADE,
    project_id   TEXT REFERENCES projects(id)   ON DELETE CASCADE,
    role         TEXT NOT NULL,
    invited_by   TEXT,
    status       TEXT NOT NULL DEFAULT 'pending',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    accepted_at  TIMESTAMPTZ
);

-- (email, target) unique while pending — one open invite per email+target.
CREATE UNIQUE INDEX invitations_pending_workspace
    ON invitations (email, workspace_id)
    WHERE status = 'pending' AND scope = 'workspace';

CREATE UNIQUE INDEX invitations_pending_project
    ON invitations (email, project_id)
    WHERE status = 'pending' AND scope = 'project';

-- Claim lookup is by (lower) email over pending rows.
CREATE INDEX invitations_email_pending
    ON invitations (email)
    WHERE status = 'pending';
