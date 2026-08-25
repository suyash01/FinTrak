DROP INDEX IF EXISTS idx_links_user_id;
DROP INDEX IF EXISTS idx_payees_user_id;
DROP INDEX IF EXISTS idx_rules_user_id;
DROP INDEX IF EXISTS idx_categories_user_id;
DROP INDEX IF EXISTS idx_transactions_user_id;
DROP INDEX IF EXISTS idx_accounts_user_id;
DROP INDEX IF EXISTS idx_payees_account_id;
DROP INDEX IF EXISTS idx_links_to_txn;
DROP INDEX IF EXISTS idx_links_from_txn;
DROP INDEX IF EXISTS idx_transactions_category_id;
DROP INDEX IF EXISTS idx_transactions_date;
DROP INDEX IF EXISTS idx_transactions_account_id;

DROP TABLE IF EXISTS links;
DROP TABLE IF EXISTS rules;
DROP TABLE IF EXISTS transactions;
DROP TABLE IF EXISTS categories;
DROP TABLE IF EXISTS payees;
DROP TABLE IF EXISTS billing_cycles;
DROP TABLE IF EXISTS accounts;
DROP TABLE IF EXISTS account_types;
DROP TABLE IF EXISTS users;

DROP EXTENSION IF EXISTS "pgcrypto";