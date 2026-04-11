-- Add payee_id column to transactions
ALTER TABLE transactions ADD COLUMN payee_id UUID REFERENCES payees(id) ON DELETE SET NULL;

-- Add payee_id column to rules
ALTER TABLE rules ADD COLUMN payee_id UUID REFERENCES payees(id) ON DELETE SET NULL;

-- Migrate existing unique payees from transactions and rules to the payees table
INSERT INTO payees (name)
SELECT DISTINCT name FROM (
    SELECT payee as name FROM transactions WHERE payee IS NOT NULL AND payee != ''
    UNION
    SELECT payee as name FROM rules WHERE payee IS NOT NULL AND payee != ''
) AS all_payees
ON CONFLICT (name) DO NOTHING;

-- Update transactions with payee_id
UPDATE transactions t
SET payee_id = p.id
FROM payees p
WHERE t.payee = p.name;

-- Update rules with payee_id
UPDATE rules r
SET payee_id = p.id
FROM payees p
WHERE r.payee = p.name;
