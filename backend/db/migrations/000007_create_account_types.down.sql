-- Re-add the type column to accounts
ALTER TABLE accounts ADD COLUMN type VARCHAR(20);

-- Backfill from account_type_id
UPDATE accounts SET type = account_type_id;

-- Add NOT NULL and CHECK constraint
ALTER TABLE accounts ALTER COLUMN type SET NOT NULL;
ALTER TABLE accounts ADD CONSTRAINT accounts_type_check CHECK (type IN ('bank', 'credit_card'));

-- Drop the FK column
ALTER TABLE accounts DROP COLUMN account_type_id;

-- Drop the account_types table
DROP TABLE IF EXISTS account_types;
