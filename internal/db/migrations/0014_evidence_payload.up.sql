-- 0014_evidence_payload.up.sql — typed payload column on the
-- evidence ledger primitive.
--
-- The evidence table (migration 0001) was always V5's append-only
-- ledger; this migration adds a structured-event slot beyond `body`
-- (free text) and `refs` (typed as a JSON array of references).
-- `payload` is the verb-level event blob — story diff for
-- story_updated entries, reviewer finding shape for review_finding
-- entries, etc. Schema additions only; append-only enforcement
-- (evidence_no_update / evidence_no_delete triggers from 0001) is
-- unchanged.

ALTER TABLE evidence
    ADD COLUMN payload JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE INDEX evidence_payload_gin ON evidence USING GIN (payload jsonb_path_ops);
