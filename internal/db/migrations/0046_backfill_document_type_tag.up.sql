-- 0046_backfill_document_type_tag.up.sql — classify pre-existing user documents
-- (sty_0ca51ef4, epic:phases-task-outputs). The documents panels filter to the KV
-- `type:document` classification and the ingest path now tags new uploads, but
-- documents created earlier carry no `type:document` tag and are hidden. Backfill
-- them — WITHOUT touching substrate: a row classified as a principle
-- (`principles:*`), reference/contract (`kind:*`), or already carrying any `type:`
-- tag is left untouched; only plain/uploaded user documents gain `type:document`.

UPDATE documents
SET tags = array_append(tags, 'type:document')
WHERE type = 'document'
  AND scope IN ('workspace', 'project')
  AND status = 'active'
  AND NOT EXISTS (
    SELECT 1 FROM unnest(tags) AS t
    WHERE t LIKE 'type:%' OR t LIKE 'principles:%' OR t LIKE 'kind:%'
  );
