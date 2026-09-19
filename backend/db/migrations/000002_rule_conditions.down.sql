-- Revert the richer rule conditions/actions added in 000002.
ALTER TABLE rules
    DROP CONSTRAINT IF EXISTS rules_account_tenant_fkey,
    DROP CONSTRAINT IF EXISTS rules_filter_category_fkey,
    DROP CONSTRAINT IF EXISTS rules_filter_payee_tenant_fkey;

ALTER TABLE rules
    DROP COLUMN IF EXISTS account_id,
    DROP COLUMN IF EXISTS filter_category_id,
    DROP COLUMN IF EXISTS filter_payee_id,
    DROP COLUMN IF EXISTS min_amount,
    DROP COLUMN IF EXISTS max_amount,
    DROP COLUMN IF EXISTS txn_type,
    DROP COLUMN IF EXISTS date_from,
    DROP COLUMN IF EXISTS date_to,
    DROP COLUMN IF EXISTS is_linked,
    DROP COLUMN IF EXISTS is_recurring,
    DROP COLUMN IF EXISTS add_tags,
    DROP COLUMN IF EXISTS notes;
