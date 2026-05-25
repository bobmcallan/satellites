-- 0013_workspace_project_seeds.up.sql — filebased seed columns on
-- workspaces + projects.
--
-- seed_md holds the markdown body shipped from the consumer repo's
-- .satellites/seeds/ tree via `satellites seed push`. Every push is a
-- full replace; the operator's repo is the source of truth, and portal
-- edits don't survive a re-push (intentional — seeds are configuration,
-- not free-form content).
--
-- seed_updated_at is the last apply time; NULL means "never seeded".
-- The store layer short-circuits when the incoming body matches the
-- stored seed_md byte-for-byte, so re-pushing an unchanged file is a
-- zero-write no-op (idempotency at the substrate, not the client).

ALTER TABLE workspaces
    ADD COLUMN seed_md         TEXT NOT NULL DEFAULT '',
    ADD COLUMN seed_updated_at TIMESTAMPTZ;

ALTER TABLE projects
    ADD COLUMN seed_md         TEXT NOT NULL DEFAULT '',
    ADD COLUMN seed_updated_at TIMESTAMPTZ;
