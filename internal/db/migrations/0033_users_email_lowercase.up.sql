-- 0033_users_email_lowercase.up.sql — epic:project-invites (sty_1a85bffd).
--
-- Normalise existing users.email to lower-case. Invitations are stored
-- lower-cased and the invite→membership claim looks users up by the
-- lower-cased email; a mixed-case users.email row therefore missed the
-- immediate claim and the invitee was never registered as a member. Going
-- forward CreateUser / UpsertOAuthUser / GetUserByEmail all normalise case;
-- this backfills rows written before that fix.
--
-- Fail loudly on a case-collision (two accounts differing only by case)
-- rather than silently collapsing them under the UNIQUE(email) constraint —
-- a human must merge those accounts first.

DO $$
DECLARE dup text;
BEGIN
    SELECT lower(email) INTO dup
    FROM users
    GROUP BY lower(email)
    HAVING count(*) > 1
    LIMIT 1;
    IF dup IS NOT NULL THEN
        RAISE EXCEPTION 'users.email case-collision on %; merge the duplicate accounts before normalising', dup;
    END IF;
END $$;

UPDATE users SET email = lower(email) WHERE email <> lower(email);
