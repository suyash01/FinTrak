ALTER TABLE loan_transfers
    DROP CONSTRAINT IF EXISTS loan_transfers_principal_check;
ALTER TABLE loan_transfers
    DROP COLUMN IF EXISTS principal;
