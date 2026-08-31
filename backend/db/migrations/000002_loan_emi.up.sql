-- Loan/EMI accounts + account closing.
--
-- * accounts.closed marks an account as closed: transactions can no longer
--   be added, removed, or edited on it (linking remains possible).
-- * loan_attachments is the junction that attaches a transaction (an EMI
--   payment, which lives on its own account) to exactly one loan account.
--   UNIQUE (transaction_id) enforces "one transaction -> one loan account"
--   at the database level.
ALTER TABLE accounts ADD COLUMN closed BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE loan_attachments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    loan_account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    transaction_id UUID NOT NULL UNIQUE REFERENCES transactions(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE (id, user_id)
);

CREATE INDEX loan_attachments_loan_idx ON loan_attachments (loan_account_id, user_id);
CREATE INDEX loan_attachments_txn_idx ON loan_attachments (transaction_id);