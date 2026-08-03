DROP INDEX IF EXISTS idx_accounts_user_id;
DROP INDEX IF EXISTS idx_transactions_user_id;
DROP INDEX IF EXISTS idx_categories_user_id;
DROP INDEX IF EXISTS idx_rules_user_id;
DROP INDEX IF EXISTS idx_payees_user_id;
DROP INDEX IF EXISTS idx_links_user_id;

ALTER TABLE links DROP COLUMN IF EXISTS user_id;
ALTER TABLE payees DROP COLUMN IF EXISTS user_id;
ALTER TABLE rules DROP COLUMN IF EXISTS user_id;
ALTER TABLE categories DROP COLUMN IF EXISTS user_id;
ALTER TABLE transactions DROP COLUMN IF EXISTS user_id;
ALTER TABLE accounts DROP COLUMN IF EXISTS user_id;

DROP TABLE IF EXISTS users;
