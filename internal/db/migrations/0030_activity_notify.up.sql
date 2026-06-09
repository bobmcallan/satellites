-- 0030_activity_notify.up.sql — add the activity:<story> SSE topic
-- (epic:dynamic-workflow-status, order:1, sty_8e21c3ec).
--
-- Engagement events flushed to the ledger (kind engagement:*) already fire the
-- story:/project: topics via satellites_notify_evidence (migration 0025). Also
-- fire a dedicated activity:<story> topic for those rows so the portal can light
-- a per-story "being worked now" indicator off a signal distinct from content
-- changes, and update one row without refetching whole fragments. Topic-only, no
-- row data — consistent with the existing bus. CREATE OR REPLACE on the function
-- keeps the existing evidence_notify trigger binding.

CREATE OR REPLACE FUNCTION satellites_notify_evidence() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.story_id IS NOT NULL AND NEW.story_id <> '' THEN
        PERFORM satellites_notify('story:' || NEW.story_id, NEW.project_id, NEW.workspace_id);
        IF NEW.kind LIKE 'engagement:%' THEN
            PERFORM satellites_notify('activity:' || NEW.story_id, NEW.project_id, NEW.workspace_id);
        END IF;
    END IF;
    IF NEW.project_id IS NOT NULL AND NEW.project_id <> '' THEN
        PERFORM satellites_notify('project:' || NEW.project_id, NEW.project_id, NEW.workspace_id);
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
