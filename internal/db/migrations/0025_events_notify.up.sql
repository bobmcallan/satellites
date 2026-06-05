-- 0025_events_notify.up.sql — SSE trigger bus (sty_b6e39eb8).
--
-- Substrate writes emit a Postgres NOTIFY on channel `satellites_events`
-- carrying a tiny topic payload — NO row data. The portal's /events SSE stream
-- forwards only the topic to the browser, which then re-fetches its own data
-- over a normal authorized GET (the doorbell, not the payload). The project /
-- workspace ids in the payload are server-side authz scope keys (they gate
-- per-user delivery) and never reach the browser.
--
-- Payload shape: {"t":"<topic>","p":"<project_id>","w":"<workspace_id>"}
-- Topics: `story:<id>` (detail / process-trace views) and `project:<id>`
-- (story-index views).

-- satellites_notify queues one NOTIFY for topic with its authz scope ids.
-- A NULL/empty topic is a no-op. NOTIFY is queued until the writing
-- transaction commits, so listeners only see committed state.
CREATE OR REPLACE FUNCTION satellites_notify(topic TEXT, project_id TEXT, workspace_id TEXT)
RETURNS void AS $$
BEGIN
    IF topic IS NULL OR topic = '' THEN
        RETURN;
    END IF;
    PERFORM pg_notify(
        'satellites_events',
        json_build_object(
            't', topic,
            'p', COALESCE(project_id, ''),
            'w', COALESCE(workspace_id, '')
        )::text
    );
END;
$$ LANGUAGE plpgsql;

-- evidence is the append-only ledger: each INSERT is a ledger append. Fire the
-- story topic (process-trace views) and the project topic (index views).
CREATE OR REPLACE FUNCTION satellites_notify_evidence() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.story_id IS NOT NULL AND NEW.story_id <> '' THEN
        PERFORM satellites_notify('story:' || NEW.story_id, NEW.project_id, NEW.workspace_id);
    END IF;
    IF NEW.project_id IS NOT NULL AND NEW.project_id <> '' THEN
        PERFORM satellites_notify('project:' || NEW.project_id, NEW.project_id, NEW.workspace_id);
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER evidence_notify AFTER INSERT ON evidence
    FOR EACH ROW EXECUTE FUNCTION satellites_notify_evidence();

-- documents: a new row (story created) or a status change (gate fired,
-- transition enacted) is the moving data the index + detail views watch. Fire
-- the story topic for a story row and the project topic for any project-scoped
-- doc. UPDATEs that don't change status are skipped (body edits still fire on
-- INSERT of a new version row in evidence/ledger paths, not here).
CREATE OR REPLACE FUNCTION satellites_notify_document() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.status IS NOT DISTINCT FROM OLD.status THEN
        RETURN NULL;
    END IF;
    IF NEW.type = 'story' THEN
        PERFORM satellites_notify('story:' || NEW.id, NEW.project_id, NEW.workspace_id);
    END IF;
    IF NEW.project_id IS NOT NULL AND NEW.project_id <> '' THEN
        PERFORM satellites_notify('project:' || NEW.project_id, NEW.project_id, NEW.workspace_id);
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER documents_notify AFTER INSERT OR UPDATE ON documents
    FOR EACH ROW EXECUTE FUNCTION satellites_notify_document();
