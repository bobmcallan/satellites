-- 0030_activity_notify.down.sql — restore satellites_notify_evidence to the
-- 0025 definition (story:/project: only, no activity: topic).

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
