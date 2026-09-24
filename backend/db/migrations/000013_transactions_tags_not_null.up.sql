-- Enforce the transaction tag-array invariant at the database boundary.
-- Older rows may predate the current application write edge, so normalize them
-- before adding NOT NULL.
UPDATE transactions
SET tags = '{}'
WHERE tags IS NULL;

ALTER TABLE transactions
    ALTER COLUMN tags SET DEFAULT '{}',
    ALTER COLUMN tags SET NOT NULL;
