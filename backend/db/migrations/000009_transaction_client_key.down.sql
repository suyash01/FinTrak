DROP INDEX IF EXISTS transactions_user_client_key;
ALTER TABLE transactions
    DROP COLUMN IF EXISTS client_key;
