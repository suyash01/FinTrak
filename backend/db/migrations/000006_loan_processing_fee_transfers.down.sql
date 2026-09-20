DROP INDEX IF EXISTS loan_transfers_tenant_to_loan_account_id;
DROP TABLE IF EXISTS loan_transfers;

ALTER TABLE loan_schedules
    DROP CONSTRAINT IF EXISTS loan_schedules_processing_fee_nonnegative;
ALTER TABLE loan_schedules
    DROP COLUMN IF EXISTS disbursal_date,
    DROP COLUMN IF EXISTS processing_fee;
