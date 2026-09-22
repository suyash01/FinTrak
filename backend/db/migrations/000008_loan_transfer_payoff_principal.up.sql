-- What a settled transfer moved in principal, so its interest is separable.
--
-- A transfer used to move exactly the source's outstanding principal. Settling a
-- loan mid-period also clears the interest accrued since its last EMI payment,
-- so the recorded amount is now a payoff: principal + accrued interest. The two
-- parts are kept apart because the principal is what the source actually owed
-- and the interest is what time cost — the split a lender's quote shows, and the
-- only way to tell a large payoff from a large principal.
--
-- Rows written before this migration moved principal only, so their principal is
-- their amount and their accrued interest is zero, which is what happened.
ALTER TABLE loan_transfers
    ADD COLUMN IF NOT EXISTS principal BIGINT NOT NULL DEFAULT 0;

UPDATE loan_transfers SET principal = amount WHERE principal = 0;

ALTER TABLE loan_transfers
    DROP CONSTRAINT IF EXISTS loan_transfers_principal_check;
ALTER TABLE loan_transfers
    ADD CONSTRAINT loan_transfers_principal_check
        CHECK (principal >= 0 AND principal <= amount);
