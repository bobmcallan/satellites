-- 0047_rebackfill_document_type_tag.up.sql — re-run the type:document backfill
-- (sty_5ceec3f1, epic:phases-task-outputs). Migration 0046 backfilled the rows
-- that existed at its deploy, but the MCP/CLI document_upsert path kept creating
-- type=document rows WITHOUT the type:document TAG (it set tags to exactly the
-- caller's set, never deriving the kvtag from the column). Those rows — e.g. the
-- nine in proj_9ff86e59 — are returned by document_list/count (which filter the
-- type COLUMN) yet hidden from the documents panels (which filter the type:document
-- TAG). The verb now auto-derives the tag, closing the divergence going forward;
-- this migration backfills the rows created since 0046. Identical predicate to
-- 0046, so it is idempotent and never touches substrate (principles:*, kind:*) or
-- already-classified (type:*) rows, and never duplicates the tag.

UPDATE documents
SET tags = array_append(tags, 'type:document')
WHERE type = 'document'
  AND scope IN ('workspace', 'project')
  AND status = 'active'
  AND NOT EXISTS (
    SELECT 1 FROM unnest(tags) AS t
    WHERE t LIKE 'type:%' OR t LIKE 'principles:%' OR t LIKE 'kind:%'
  );
