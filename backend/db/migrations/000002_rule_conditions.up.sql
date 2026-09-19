-- Rule conditions & actions (richer rules).
--
-- Rules previously matched only a description substring and could only assign a
-- category + payee. This migration adds optional conditions (all ANDed) and
-- richer actions:
--   * conditions: account, category, payee, amount range (minor units),
--     transaction type, date window, and current linked/recurring state
--     (NULL = condition not set; for the two booleans NULL = "either")
--   * actions: add_tags (tags to union onto the transaction) and notes
--     (appended to any existing notes)
-- Every condition column is nullable so existing rules keep matching exactly
-- as before. category_id (the action) stays NOT NULL, which also keeps the
-- "first matching rule wins" guard in ApplyRules (category_id IS NULL) intact.
ALTER TABLE rules
    ADD COLUMN IF NOT EXISTS account_id UUID,
    ADD COLUMN IF NOT EXISTS filter_category_id UUID,
    ADD COLUMN IF NOT EXISTS filter_payee_id UUID,
    ADD COLUMN IF NOT EXISTS min_amount BIGINT,
    ADD COLUMN IF NOT EXISTS max_amount BIGINT,
    ADD COLUMN IF NOT EXISTS txn_type VARCHAR(10) CHECK (txn_type IN ('debit', 'credit')),
    ADD COLUMN IF NOT EXISTS date_from DATE,
    ADD COLUMN IF NOT EXISTS date_to DATE,
    ADD COLUMN IF NOT EXISTS is_linked BOOLEAN,
    ADD COLUMN IF NOT EXISTS is_recurring BOOLEAN,
    ADD COLUMN IF NOT EXISTS add_tags TEXT[] DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS notes TEXT DEFAULT '';

-- Tenant-scoped references: a rule's account/payee conditions must belong to
-- the same user. Category conditions may reference a global (admin) category,
-- so that one is a plain FK mirroring rules.category_id.
ALTER TABLE rules
    ADD CONSTRAINT rules_account_tenant_fkey
        FOREIGN KEY (user_id, account_id)
        REFERENCES accounts (user_id, id) ON DELETE SET NULL (account_id);

ALTER TABLE rules
    ADD CONSTRAINT rules_filter_category_fkey
        FOREIGN KEY (filter_category_id)
        REFERENCES categories (id) ON DELETE SET NULL;

ALTER TABLE rules
    ADD CONSTRAINT rules_filter_payee_tenant_fkey
        FOREIGN KEY (user_id, filter_payee_id)
        REFERENCES payees (user_id, id) ON DELETE SET NULL (filter_payee_id);
