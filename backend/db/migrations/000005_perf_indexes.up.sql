-- Performance indexes for hot read paths.
--
-- 1. Duplicate detection (import/validate) and the billing-cycle helpers look up
--    a transaction's account history by (account_id, user_id, date).
-- 2. Transfer suggestions join on equality of user_id/type/amount with a small
--    date window; (user_id, type, amount, date) makes the amount equality
--    selective and supports the date range.
-- 3. Cashback suggestions look up same-account debits within 90 days by
--    (account_id, user_id, type, date).
-- 4. Billing-cycle generation frequently checks for unassigned transactions on
--    an account; a partial index keeps that check cheap.
-- 5. Billing-cycle lookups filter on (account_id, user_id).

CREATE INDEX IF NOT EXISTS transactions_account_user_date_idx
    ON transactions (account_id, user_id, date);

CREATE INDEX IF NOT EXISTS transactions_user_type_amount_date_idx
    ON transactions (user_id, type, amount, date);

CREATE INDEX IF NOT EXISTS transactions_account_user_type_date_idx
    ON transactions (account_id, user_id, type, date);

CREATE INDEX IF NOT EXISTS transactions_unassigned_account_idx
    ON transactions (account_id, user_id) WHERE billing_cycle_id IS NULL;

CREATE INDEX IF NOT EXISTS billing_cycles_account_user_idx
    ON billing_cycles (account_id, user_id);
