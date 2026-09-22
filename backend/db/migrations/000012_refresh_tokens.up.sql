-- Server-side refresh sessions: rotation, reuse detection and revocation.
--
-- Refresh tokens used to be stateless JWTs: a copied cookie kept re-arming
-- access tokens for the session's whole 30-day deadline, and no logout (or any
-- other event) could end it. Every issued refresh token now has a row, so the
-- server holds the session state that revocation needs.
--
-- Only the SHA-256 of the token is stored (the token itself is never
-- persisted), `family_id` groups one session's rotation chain, `revoked_at`
-- marks a token as spent and `replaced_by` points at its successor. Presenting
-- a revoked token is proof the value leaked — the only legitimate holder was
-- handed the successor when the cookie was replaced — and revokes the family.
CREATE TABLE IF NOT EXISTS refresh_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    family_id UUID NOT NULL,
    issued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    replaced_by UUID
);

-- Lookups are by hash (unique index above) and revocations are by family;
-- the user index serves the per-user cleanup path.
CREATE INDEX IF NOT EXISTS refresh_tokens_family_id_idx ON refresh_tokens (family_id);
CREATE INDEX IF NOT EXISTS refresh_tokens_user_id_idx ON refresh_tokens (user_id);

-- An explicitly cleared billing cycle must not be re-attached by the read-side
-- back-fill (T1-BackendTxnCore-4). Clearing the cycle sets billing_cycle_id back
-- to NULL, which is exactly what "never assigned yet" looks like, so the
-- back-fill kept re-deriving the cycle the user had just removed. The flag
-- records the user's intent per row instead of inferring it.
ALTER TABLE transactions
    ADD COLUMN IF NOT EXISTS billing_cycle_detached BOOLEAN NOT NULL DEFAULT FALSE;
