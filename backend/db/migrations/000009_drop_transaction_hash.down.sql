ALTER TABLE transactions ADD COLUMN IF NOT EXISTS hash VARCHAR(64);
CREATE INDEX IF NOT EXISTS idx_transactions_hash ON transactions(hash);
