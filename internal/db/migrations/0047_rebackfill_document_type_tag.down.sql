-- 0047_rebackfill_document_type_tag.down.sql — intentional no-op.
-- Like 0046, this is a one-time DATA backfill that cannot be perfectly inverted:
-- a backfilled `type:document` tag is indistinguishable from one an author, the
-- ingest path, or the upsert verb wrote, so a blanket removal would corrupt
-- legitimately-classified documents. Reversing the migration leaves the data in place.
SELECT 1;
