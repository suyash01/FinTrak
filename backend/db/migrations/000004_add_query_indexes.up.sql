-- Add indexes for the dominant query patterns.
--
-- The base schema only carries UNIQUE (id, user_id) indexes, which are useless
-- for queries that predicate on user_id/account_id first:
--
--   * GET /transactions and its COUNT(*) — WHERE user_id = $1
--     ORDER BY date DESC LIMIT/OFFSET
--   * Import/validate duplicate snapshots and billing-cycle aggregates —
--     WHERE account_id = $1 AND user_id = $2
--   * the per-row is_linked EXISTS in the transaction list —
--     SELECT 1 FROM links WHERE from_txn_id = t.id OR to_txn_id = t.id
--     (two single-column indexes let Postgres combine them with a BitmapOr)
--   * FK-ownership EXISTS checks and LEFT JOINs on category_id/payee_id
--   * rules/payees/categories listing — WHERE user_id = $1
--   * GetLinks — WHERE user_id = $1 ORDER BY created_at DESC

CREATE INDEX transactions_user_date_idx
    ON transactions (user_id, date DESC);

CREATE INDEX transactions_account_user_idx
    ON transactions (account_id, user_id);

CREATE INDEX transactions_category_idx
    ON transactions (category_id) WHERE category_id IS NOT NULL;

CREATE INDEX transactions_payee_idx
    ON transactions (payee_id) WHERE payee_id IS NOT NULL;

CREATE INDEX transactions_billing_cycle_idx
    ON transactions (billing_cycle_id) WHERE billing_cycle_id IS NOT NULL;

CREATE INDEX links_from_txn_idx
    ON links (from_txn_id);

CREATE INDEX links_to_txn_idx
    ON links (to_txn_id);

CREATE INDEX links_user_created_idx
    ON links (user_id, created_at DESC);

CREATE INDEX rules_user_idx
    ON rules (user_id);

CREATE INDEX payees_user_idx
    ON payees (user_id);

CREATE INDEX categories_user_idx
    ON categories (user_id) WHERE user_id IS NOT NULL;