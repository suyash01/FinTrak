-- Store transaction amounts as integer minor units (cents) instead of
-- DECIMAL(15,2), so aggregation, balance, and transfer-scoring math use exact
-- integer arithmetic with no float rounding drift. Existing values are scaled
-- losslessly: DECIMAL(15,2) * 100 always fits a BIGINT (max ~9.2e18).
ALTER TABLE transactions
    ALTER COLUMN amount TYPE BIGINT
    USING ROUND(amount * 100)::BIGINT;
