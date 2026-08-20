-- Billing cycles for credit-card accounts.
--
-- A billing cycle is an explicit, persisted period (start_date..end_date) that
-- transactions are attached to via transactions.billing_cycle_id. Cycles are
-- auto-generated from the account's billing_day (one per month) and transactions
-- are pre-assigned to the cycle matching their date, but the assignment can be
-- changed manually.
CREATE TABLE IF NOT EXISTS billing_cycles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    label VARCHAR(255) DEFAULT '',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE (account_id, start_date)
);

CREATE INDEX IF NOT EXISTS idx_billing_cycles_account_id ON billing_cycles(account_id);
CREATE INDEX IF NOT EXISTS idx_billing_cycles_user_id ON billing_cycles(user_id);

-- Attach transactions to a billing cycle. ON DELETE SET NULL keeps the
-- transaction when a cycle is removed.
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS billing_cycle_id UUID REFERENCES billing_cycles(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_transactions_billing_cycle_id ON transactions(billing_cycle_id);