-- Reverts 000007_amount_to_cents: converts the integer cents back to
-- DECIMAL(15,2) major units.
ALTER TABLE transactions
    ALTER COLUMN amount TYPE DECIMAL(15,2)
    USING (amount::DECIMAL(15,2) / 100);
