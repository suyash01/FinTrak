-- Only the index is added by the up migration, so dropping it restores the
-- previous schema exactly (no data is touched).
DROP INDEX IF EXISTS transactions_tenant_date;
