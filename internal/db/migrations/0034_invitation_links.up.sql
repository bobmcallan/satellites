-- 0034_invitation_links.up.sql — epic:project-invites (sty_8557f770).
--
-- Extend invitations to support link invites: a secret token, not bound to a
-- pre-known email, that anyone authenticated can redeem to join the target at
-- the invited role. Email becomes nullable (link invites have no email);
-- expires_at bounds the link's validity; accepted_by records the redeemer.
-- The existing email-claim path and its partial-unique indexes are untouched.

ALTER TABLE invitations ALTER COLUMN email DROP NOT NULL;
ALTER TABLE invitations ADD COLUMN IF NOT EXISTS token       TEXT;
ALTER TABLE invitations ADD COLUMN IF NOT EXISTS expires_at  TIMESTAMPTZ;
ALTER TABLE invitations ADD COLUMN IF NOT EXISTS accepted_by TEXT;

-- A token is globally unique when present; email invites leave it NULL.
CREATE UNIQUE INDEX IF NOT EXISTS invitations_token
    ON invitations (token)
    WHERE token IS NOT NULL;
