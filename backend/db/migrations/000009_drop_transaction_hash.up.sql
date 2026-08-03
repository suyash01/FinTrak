-- Dedup hash is no longer calculated or used
DROP INDEX IF EXISTS idx_transactions_hash;
ALTER TABLE transactions DROP COLUMN IF EXISTS hash;
