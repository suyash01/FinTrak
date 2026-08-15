-- Add a billing (statement) day to accounts. It is meaningful for credit
-- cards, where it defines the day of each month that the statement is cut, and
-- is used to compute the total outstanding for the current billing cycle. It is
-- nullable and unused for bank accounts.
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS billing_day INT;