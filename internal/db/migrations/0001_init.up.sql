-- 0001_init.up.sql — V5 four primitives + append-only ledger
-- See docs/ARCHITECTURE.md for the schema rationale.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- stories: operator's requirement
CREATE TABLE stories (
    id                  TEXT PRIMARY KEY,
    project_id          TEXT NOT NULL,
    title               TEXT NOT NULL,
    body                TEXT NOT NULL DEFAULT '',
    acceptance_criteria TEXT NOT NULL DEFAULT '',
    status              TEXT NOT NULL DEFAULT 'backlog',
    priority            TEXT NOT NULL DEFAULT 'medium',
    category            TEXT NOT NULL DEFAULT 'feature',
    tags                TEXT[] NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX stories_project_status ON stories (project_id, status);
CREATE INDEX stories_tags_gin       ON stories USING GIN (tags);

-- tools: operator-authored skills/practices/abilities
CREATE TABLE tools (
    id          TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL,
    name        TEXT NOT NULL,
    body        TEXT NOT NULL DEFAULT '',
    kind        TEXT NOT NULL DEFAULT 'skill',
    scope       TEXT NOT NULL DEFAULT 'project',
    story_type  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX tools_project_name ON tools (project_id, name);
CREATE INDEX        tools_scope        ON tools (scope);

-- reviews: operator-authored reviewer agent configs
CREATE TABLE reviews (
    id          TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL,
    name        TEXT NOT NULL,
    body        TEXT NOT NULL DEFAULT '',
    scope       TEXT NOT NULL DEFAULT 'project',
    story_id    TEXT,
    story_type  TEXT,
    is_active   BOOLEAN NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX reviews_project_name ON reviews (project_id, name);
CREATE INDEX        reviews_scope        ON reviews (scope);
CREATE INDEX        reviews_active       ON reviews (is_active) WHERE is_active = true;

-- evidence: append-only ledger
CREATE TABLE evidence (
    id          TEXT PRIMARY KEY,
    story_id    TEXT NOT NULL,
    kind        TEXT NOT NULL,
    body        TEXT NOT NULL DEFAULT '',
    refs        JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by  TEXT NOT NULL DEFAULT ''
);

CREATE INDEX evidence_story_created ON evidence (story_id, created_at);
CREATE INDEX evidence_kind          ON evidence (kind);
CREATE INDEX evidence_refs_gin      ON evidence USING GIN (refs jsonb_path_ops);

-- Append-only enforcement on evidence: substrate invariant.
CREATE OR REPLACE FUNCTION evidence_append_only() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'evidence is append-only (no % allowed)', TG_OP;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER evidence_no_update BEFORE UPDATE ON evidence
    FOR EACH ROW EXECUTE FUNCTION evidence_append_only();

CREATE TRIGGER evidence_no_delete BEFORE DELETE ON evidence
    FOR EACH ROW EXECUTE FUNCTION evidence_append_only();
