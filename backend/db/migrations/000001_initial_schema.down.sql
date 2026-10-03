-- Drop the whole schema.
--
-- This is the only down migration, and it is all-or-nothing: it removes
-- everything the baseline created rather than unwinding anything, because the
-- baseline is applied to an empty database. The incremental history this
-- baseline replaced had repairs that were never reversible either -- merging
-- duplicate payees and re-pointing their references, nulling a cross-account
-- billing-cycle reference -- and on a fresh database none of them run at all.
--
-- The application never migrates down (see db.RunMigrations), so this file
-- exists for a deliberate teardown of a disposable database.
DROP TABLE IF EXISTS rules;
DROP TABLE IF EXISTS recurring_attachments;
DROP TABLE IF EXISTS recurring_series_terms;
DROP TABLE IF EXISTS recurring_series;
DROP TABLE IF EXISTS loan_disbursements;
DROP TABLE IF EXISTS loan_transfers;
DROP TABLE IF EXISTS loan_schedules;
DROP TABLE IF EXISTS links;
DROP TABLE IF EXISTS loan_attachments;
DROP TABLE IF EXISTS transactions;
DROP TABLE IF EXISTS billing_cycles;
DROP TABLE IF EXISTS payees;
DROP TABLE IF EXISTS categories;
DROP TABLE IF EXISTS category_groups;
DROP TABLE IF EXISTS accounts;
DROP TABLE IF EXISTS account_types;
DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS users;

DROP FUNCTION IF EXISTS enforce_transaction_not_loan_account();

DROP EXTENSION IF EXISTS "pgcrypto";