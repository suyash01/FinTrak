-- Server-side sessions: one revocable row per login.
--
-- id IS refresh_tokens.family_id, deliberately, rather than a new column plus a
-- foreign key to maintain. A session *is* one rotation family: family_id is
-- minted at login, is stable across every rotation, and is never reused, so
-- keying on it needs no join to get from a token to the session it names.
--
-- expires_at is the family's absolute deadline -- the value refresh_tokens
-- already carries on every row. Rotation is capped at the presented token's
-- expiry, so every row in a family shares one deadline and MAX(expires_at)
-- backfills it exactly.
--
-- user_agent is what the session list shows the user to identify a device, and
-- last_seen is written on the refresh path only (never on the read path, which
-- every authenticated request takes).
CREATE TABLE IF NOT EXISTS sessions (
    id          UUID PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    user_agent  TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS sessions_user_id_idx ON sessions (user_id);

-- Backfill every session that predates this table, so a token already in a
-- browser stays revocable across the deploy instead of becoming unendable until
-- it expires. Their user_agent is empty and their timestamps are derived, which
-- the list renders honestly as "unknown device" and the family's own activity.
--
-- revoked_at is set for a family that is already dead: NOT EXISTS over
-- refresh_tokens WHERE revoked_at IS NULL, i.e. no live token remains in it.
-- It is deliberately NOT "any revoked token" -- rotation marks every spent
-- token revoked as a matter of course, so that rule would revoke every session
-- on the planet. A logged-out family and a family killed by reuse detection
-- have every row revoked, and those are the two that must not appear in the
-- session list as live devices the user is invited to revoke.
--
-- This predicate is NOT covered end to end by CI: backend/integration_test.go
-- migrates an EMPTY database, so the backfill always selects zero rows there
-- and nothing asserts a dead family backfills as revoked. It is pinned only by
-- the rule above and by the semantics of the columns it reads.
--
-- WHERE s.id IS NULL is load-bearing, not an optimisation. sessions.id is the
-- PRIMARY KEY and this INSERT has no ON CONFLICT clause, so a family that
-- already holds a row would raise a duplicate-key error and abort the ENTIRE
-- migration -- leaving the database on the old schema version with every other
-- later migration blocked behind it. A family can already hold a row on a
-- re-run, and while this migration and the per-login insert ship in the same
-- deploy, one written by the new binary before the backfill reached that family
-- would do it on the first run too. Removing this predicate reintroduces a
-- deploy-time migration abort; it does not merely overwrite a row.
INSERT INTO sessions (id, user_id, created_at, last_seen, expires_at, revoked_at)
SELECT r.family_id, r.user_id, MIN(r.issued_at), MAX(r.issued_at), MAX(r.expires_at),
       CASE WHEN NOT EXISTS (
           SELECT 1 FROM refresh_tokens live
           WHERE live.family_id = r.family_id AND live.revoked_at IS NULL
       ) THEN MAX(r.revoked_at) END
FROM refresh_tokens r
WHERE NOT EXISTS (SELECT 1 FROM sessions s WHERE s.id = r.family_id)
GROUP BY r.family_id, r.user_id;