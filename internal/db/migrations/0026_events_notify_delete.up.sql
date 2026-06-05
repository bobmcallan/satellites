-- 0026_events_notify_delete.up.sql — SSE trigger bus: emit on document removal.
--
-- 0025 added NOTIFY triggers for INSERT/UPDATE on documents, but stories are
-- removed via HardDelete (a real DELETE, not a soft-delete status flip), so a
-- removed story fired no NOTIFY and the live story index (sty_8f69be8b) could
-- not drop the row. Add an AFTER DELETE trigger that notifies the same topics
-- from the OLD row so removals broadcast like creates and status changes.

CREATE OR REPLACE FUNCTION satellites_notify_document_delete() RETURNS TRIGGER AS $$
BEGIN
    IF OLD.type = 'story' THEN
        PERFORM satellites_notify('story:' || OLD.id, OLD.project_id, OLD.workspace_id);
    END IF;
    IF OLD.project_id IS NOT NULL AND OLD.project_id <> '' THEN
        PERFORM satellites_notify('project:' || OLD.project_id, OLD.project_id, OLD.workspace_id);
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER documents_notify_delete AFTER DELETE ON documents
    FOR EACH ROW EXECUTE FUNCTION satellites_notify_document_delete();
