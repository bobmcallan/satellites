-- 0048_rebackfill_document_type_tag_kind.up.sql — narrow the type:document
-- backfill predicate (sty_5711ab3e). Migrations 0046/0047 excluded kind:* rows
-- from the backfill, treating any kind:* tag as substrate. But a type=document
-- row that carries a descriptive kind:* facet (e.g. kind:reference) IS a project
-- document and belongs in the documents panel — excluding it left such rows
-- returned by document_list/count (which filter the type COLUMN) yet hidden from
-- the panels (which filter the type:document TAG). The upsert verb now derives the
-- tag for these rows going forward (hasExplicitClassification drops kind:*); this
-- migration backfills the rows created under the old predicate, e.g. the six in
-- proj_567bbfbf. Predicate now excludes only already-classified (type:*) and
-- principle substrate (principles:*) — kind:* is no longer excluded. Idempotent:
-- rows that already carry type:document are skipped, so the tag is never duplicated.

UPDATE documents
SET tags = array_append(tags, 'type:document')
WHERE type = 'document'
  AND scope IN ('workspace', 'project')
  AND status = 'active'
  AND NOT EXISTS (
    SELECT 1 FROM unnest(tags) AS t
    WHERE t LIKE 'type:%' OR t LIKE 'principles:%'
  );
