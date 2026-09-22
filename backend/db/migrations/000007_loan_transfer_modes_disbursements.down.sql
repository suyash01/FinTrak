DROP TABLE IF EXISTS loan_disbursements;

-- Restore the two-shape boolean: everything that was not an "opens" transfer
-- recast its target.
ALTER TABLE loan_transfers
    ADD COLUMN IF NOT EXISTS recasts_target BOOLEAN NOT NULL DEFAULT TRUE;
UPDATE loan_transfers SET recasts_target = (mode <> 'opens');

ALTER TABLE loan_transfers
    DROP CONSTRAINT IF EXISTS loan_transfers_mode_check;
ALTER TABLE loan_transfers
    DROP COLUMN IF EXISTS mode;
