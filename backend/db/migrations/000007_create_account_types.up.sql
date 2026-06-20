-- Create account_types reference table
CREATE TABLE IF NOT EXISTS account_types (
    id VARCHAR(30) PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    positive_txn_type VARCHAR(6) NOT NULL CHECK (positive_txn_type IN ('credit', 'debit'))
);

-- Seed default types
INSERT INTO account_types (id, name, positive_txn_type) VALUES
    ('bank', 'Bank Account', 'credit'),
    ('credit_card', 'Credit Card', 'debit')
ON CONFLICT DO NOTHING;

-- Add FK column to accounts
ALTER TABLE accounts ADD COLUMN account_type_id VARCHAR(30) REFERENCES account_types(id);

-- Backfill from existing type column
UPDATE accounts SET account_type_id = type;

-- Make it NOT NULL after backfill
ALTER TABLE accounts ALTER COLUMN account_type_id SET NOT NULL;

-- Drop old type column (and its CHECK constraint)
ALTER TABLE accounts DROP COLUMN type;
