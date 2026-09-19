-- Tenant index for recurring_attachments.
--
-- It is the only tenant-scoped junction table shipped without its
-- (user_id, <parent>_id) index: loan_attachments carries both of its columns,
-- recurring_series_terms and links carry theirs. Without it every series-scoped
-- read of the junction scans the table — the attached-transaction list and the
-- attached-transaction fetch in handlers/recurring.go both filter
-- ra.user_id/ra.series_id, the series list runs a correlated
-- SELECT COUNT(*) ... WHERE ra.series_id = rs.id once per series row, and the
-- ON DELETE CASCADE from recurring_series has to locate its referencing rows.
-- The series column leads the index after user_id, matching those predicates.
CREATE INDEX IF NOT EXISTS recurring_attachments_tenant_series_id
    ON recurring_attachments (user_id, series_id);
