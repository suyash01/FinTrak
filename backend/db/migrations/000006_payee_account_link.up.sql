-- Add account_id to payees
ALTER TABLE payees ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE SET NULL;
CREATE UNIQUE INDEX idx_payees_account_id ON payees(account_id) WHERE account_id IS NOT NULL;

-- 1. Try to link existing payees where name matches account name exactly (case insensitive)
UPDATE payees p
SET account_id = a.id
FROM accounts a
WHERE LOWER(p.name) = LOWER(a.name) AND p.account_id IS NULL;

-- 2. For accounts that still don't have a linked payee, create one
INSERT INTO payees (id, name, account_id, created_at, updated_at)
SELECT gen_random_uuid(), name, id, NOW(), NOW()
FROM accounts a
WHERE NOT EXISTS (SELECT 1 FROM payees p WHERE p.account_id = a.id);
