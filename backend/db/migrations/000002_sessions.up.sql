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
-- WHERE s.id IS NULL makes this idempotent alongside the per-login insert that
-- arrives on the same deploy: a family the running server has already written
-- keeps the row it wrote (with its real user agent) rather than having its
-- timestamps overwritten by derived ones.
INSERT INTO sessions (id, user_id, created_at, last_seen, expires_at)
SELECT r.family_id, r.user_id, MIN(r.issued_at), MAX(r.issued_at), MAX(r.expires_at)
FROM refresh_tokens r
WHERE NOT EXISTS (SELECT 1 FROM sessions s WHERE s.id = r.family_id)
GROUP BY r.family_id, r.user_id;