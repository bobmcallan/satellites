-- 0026_events_notify_delete.down.sql

DROP TRIGGER IF EXISTS documents_notify_delete ON documents;
DROP FUNCTION IF EXISTS satellites_notify_document_delete();
