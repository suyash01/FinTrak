-- Balance-transfer modes, and the bank credit a loan's disbursement is checked
-- against.
--
-- 000006 gave a transfer two shapes, told apart by recasts_target: it either
-- added the source's balance to the target's remaining installments, or it
-- created the target's schedule from the amount. Both assume the target's *debt*
-- absorbs the balance. A takeover is the third shape and the one a refinance
-- actually uses: the target loan keeps amortizing its own principal, and the
-- amount is paid *out of the target's disbursement* to settle the source — so it
-- reduces the cash the target released instead of increasing what it owes.
--
-- mode names all three cases:
--   recast   — the target's installments due after the transfer absorb the amount
--   opens    — the transfer created the target's schedule; the amount is its principal
--   takeover — the target's own table stands; the amount is a disbursement outflow
-- Rows written before this migration keep the behavior they were written with.
ALTER TABLE loan_transfers
    ADD COLUMN IF NOT EXISTS mode TEXT NOT NULL DEFAULT 'recast';

UPDATE loan_transfers
   SET mode = CASE WHEN recasts_target THEN 'recast' ELSE 'opens' END;

ALTER TABLE loan_transfers
    DROP CONSTRAINT IF EXISTS loan_transfers_mode_check;
ALTER TABLE loan_transfers
    ADD CONSTRAINT loan_transfers_mode_check CHECK (mode IN ('recast', 'opens', 'takeover'));

ALTER TABLE loan_transfers
    DROP COLUMN IF EXISTS recasts_target;

-- The bank credit that released a loan, so the disbursement the schedule implies
-- (principal - processing fee - takeovers it funded) can be reconciled against
-- what actually landed in the borrower's account — the one number a sanction
-- letter and a bank statement have to agree on.
--
-- One credit per loan and one loan per credit: a transaction that is already an
-- EMI payment, or another loan's disbursement, cannot be this one's.
CREATE TABLE IF NOT EXISTS loan_disbursements (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    loan_account_id UUID NOT NULL,
    transaction_id UUID NOT NULL UNIQUE,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (user_id, id),
    UNIQUE (user_id, loan_account_id),
    CONSTRAINT loan_disbursements_account_tenant_fkey
        FOREIGN KEY (user_id, loan_account_id)
        REFERENCES accounts (user_id, id) ON DELETE CASCADE,
    CONSTRAINT loan_disbursements_transaction_tenant_fkey
        FOREIGN KEY (user_id, transaction_id)
        REFERENCES transactions (user_id, id) ON DELETE CASCADE
);
