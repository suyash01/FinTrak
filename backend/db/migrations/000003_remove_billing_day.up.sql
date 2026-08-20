-- Remove the per-account billing day. Billing cycles for credit-card accounts
-- are now always generated on the 1st of each month; the account-level setting
-- is no longer needed.
ALTER TABLE accounts DROP COLUMN IF EXISTS billing_day;