-- 0042_variables_secret.down.sql — drop the secret flag. Secret rows revert to
-- being indistinguishable from plain variables; the operator should delete any
-- secret rows before downgrading so a key is not exposed by the un-redacted
-- read verbs.
BEGIN;

ALTER TABLE variables DROP COLUMN IF EXISTS secret;

COMMIT;
