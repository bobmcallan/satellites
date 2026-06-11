-- 0033_users_email_lowercase.down.sql
--
-- Lower-casing is lossy — the original mixed-case form is not recoverable —
-- so the down is intentionally a no-op. Normalised emails remain valid.
SELECT 1;
