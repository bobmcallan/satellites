-- 0014_evidence_payload.down.sql — drop the payload column.

DROP INDEX IF EXISTS evidence_payload_gin;
ALTER TABLE evidence DROP COLUMN IF EXISTS payload;
