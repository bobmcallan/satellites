-- 0025_events_notify.down.sql — drop the SSE trigger bus NOTIFY plumbing.

DROP TRIGGER IF EXISTS documents_notify ON documents;
DROP TRIGGER IF EXISTS evidence_notify ON evidence;
DROP FUNCTION IF EXISTS satellites_notify_document();
DROP FUNCTION IF EXISTS satellites_notify_evidence();
DROP FUNCTION IF EXISTS satellites_notify(TEXT, TEXT, TEXT);
