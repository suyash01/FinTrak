-- Restore the pre-000004 state: drop the added query indexes.

DROP INDEX categories_user_idx;
DROP INDEX payees_user_idx;
DROP INDEX rules_user_idx;
DROP INDEX links_user_created_idx;
DROP INDEX links_to_txn_idx;
DROP INDEX links_from_txn_idx;
DROP INDEX transactions_billing_cycle_idx;
DROP INDEX transactions_payee_idx;
DROP INDEX transactions_category_idx;
DROP INDEX transactions_account_user_idx;
DROP INDEX transactions_user_date_idx;