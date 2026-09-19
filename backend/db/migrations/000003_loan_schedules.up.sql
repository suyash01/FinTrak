-- Optional amortization schedule for a Loan / EMI account. A loan account holds
-- no transactions of its own and an attached EMI payment carries no principal/
-- interest split — the account's "repaid" balance is just the sum of its
-- attachments. A schedule records the loan's terms so the amortization table can
-- be generated (each installment decomposed into principal and interest, the
-- final one absorbing rounding so the loan repays exactly) and matched against
-- the attached EMI payments in date order.
--
-- At most one schedule per loan account. Money is stored as integer minor units
-- (cents) and the rate as integer basis points (950 = 9.50% p.a.), so no money
-- arithmetic ever runs in float64.
CREATE TABLE IF NOT EXISTS loan_schedules (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    loan_account_id UUID NOT NULL,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    principal BIGINT NOT NULL CHECK (principal > 0),
    annual_rate_bps INTEGER NOT NULL CHECK (annual_rate_bps >= 0),
    tenure_months INTEGER NOT NULL CHECK (tenure_months BETWEEN 1 AND 600),
    start_date DATE NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (user_id, id),
    UNIQUE (user_id, loan_account_id),
    CONSTRAINT loan_schedules_account_tenant_fkey
        FOREIGN KEY (user_id, loan_account_id)
        REFERENCES accounts (user_id, id) ON DELETE CASCADE
);
