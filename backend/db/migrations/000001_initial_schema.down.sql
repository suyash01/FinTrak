DROP INDEX IF EXISTS idx_links_to_txn;
DROP INDEX IF EXISTS idx_links_from_txn;
DROP INDEX IF EXISTS idx_transactions_hash;
DROP INDEX IF EXISTS idx_transactions_category_id;
DROP INDEX IF EXISTS idx_transactions_date;
DROP INDEX IF EXISTS idx_transactions_account_id;

DROP TABLE IF EXISTS links;
DROP TABLE IF EXISTS rules;
DROP TABLE IF EXISTS transactions;
DROP TABLE IF EXISTS categories;
DROP TABLE IF EXISTS accounts;

DROP EXTENSION IF EXISTS "pgcrypto";
