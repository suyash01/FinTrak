-- Processing fee, disbursal date, and balance transfers for Loan / EMI accounts.
--
-- These three columns/tables all hang off the amortization schedule 000003
-- introduced, because that schedule is the only place a loan's principal is
-- modeled at all: an attached EMI payment carries no principal/interest split,
-- so every derived figure comes from the terms plus these rows.
--
--   * processing_fee — what the lender charged, in minor units like every other
--     money column. It is reference data: the amortization table is generated
--     over the whole principal and the outstanding balance runs against that,
--     so recording the fee never moves a single installment. A lender that
--     finances its charges instead has them entered in the principal.
--   * disbursal_date — when the money was actually released. The first
--     installment covers disbursal -> first due date; when that period is not a
--     whole anchored month (disbursed on the 20th, first EMI on the 5th) the
--     handler charges day-count interest for the broken period, so installment
--     1 splits differently from every later one. NULL keeps the previous
--     behavior exactly: the first period is one month.
--   * loan_transfers — one row per balance transfer / refinance. The source
--     loan is settled at its outstanding balance on transfer_date and the
--     target loan absorbs that amount, recasting its remaining installments.
--     Both loans' tables are derived from these rows, so deleting one reverts
--     both sides rather than leaving half-applied balances behind.
--
-- UNIQUE (user_id, from_loan_account_id) encodes "a loan can be transferred out
-- at most once" in the database: once settled it has no remaining balance to
-- move again, and the constraint turns a concurrent double-transfer into a
-- constraint violation the handler maps to a 409 instead of two recasts. It
-- also indexes the source-side lookup; the target side gets its own index.
--
-- recasts_target separates the two shapes a transfer can have on the receiving
-- loan. TRUE (the usual case) means the target already had a schedule and the
-- transferred amount is an addition to the balance it is still repaying, so the
-- installments after transfer_date are recast over it. FALSE means the transfer
-- created the target's schedule — the target had no terms of its own, so the
-- amount is that schedule's principal and must not be added on top of it a
-- second time. Both are read back by handlers/loan.go when the table is
-- regenerated, so the flag is data, not bookkeeping.
ALTER TABLE loan_schedules
    ADD COLUMN IF NOT EXISTS processing_fee BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS disbursal_date DATE;

ALTER TABLE loan_schedules
    DROP CONSTRAINT IF EXISTS loan_schedules_processing_fee_nonnegative;
ALTER TABLE loan_schedules
    ADD CONSTRAINT loan_schedules_processing_fee_nonnegative CHECK (processing_fee >= 0);

CREATE TABLE IF NOT EXISTS loan_transfers (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    from_loan_account_id UUID NOT NULL,
    to_loan_account_id UUID NOT NULL,
    amount BIGINT NOT NULL CHECK (amount > 0),
    transfer_date DATE NOT NULL,
    recasts_target BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (user_id, id),
    UNIQUE (user_id, from_loan_account_id),
    CONSTRAINT loan_transfers_distinct_accounts
        CHECK (from_loan_account_id <> to_loan_account_id),
    CONSTRAINT loan_transfers_from_account_tenant_fkey
        FOREIGN KEY (user_id, from_loan_account_id)
        REFERENCES accounts (user_id, id) ON DELETE CASCADE,
    CONSTRAINT loan_transfers_to_account_tenant_fkey
        FOREIGN KEY (user_id, to_loan_account_id)
        REFERENCES accounts (user_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS loan_transfers_tenant_to_loan_account_id
    ON loan_transfers (user_id, to_loan_account_id);
