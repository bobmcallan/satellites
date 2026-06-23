-- 0046_backfill_document_type_tag.down.sql — intentional no-op.
-- This is a one-time DATA backfill and cannot be perfectly inverted: a
-- backfilled `type:document` tag is indistinguishable from one an author or the
-- ingest path wrote, so a blanket removal would corrupt legitimately-classified
-- documents. Reversing the migration therefore leaves the data in place.
SELECT 1;
