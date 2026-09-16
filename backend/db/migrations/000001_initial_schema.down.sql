DROP TABLE IF EXISTS recurring_attachments;
DROP TABLE IF EXISTS recurring_series_terms;
DROP TABLE IF EXISTS recurring_series;
DROP TABLE IF EXISTS links;
DROP TABLE IF EXISTS rules;
DROP TABLE IF EXISTS loan_attachments;
DROP TABLE IF EXISTS transactions;
DROP TABLE IF EXISTS billing_cycles;
DROP TABLE IF EXISTS payees;
DROP TABLE IF EXISTS categories;
DROP TABLE IF EXISTS category_groups;
DROP TABLE IF EXISTS accounts;
DROP TABLE IF EXISTS account_types;
DROP TABLE IF EXISTS users;

DROP FUNCTION IF EXISTS enforce_transaction_not_loan_account();

DROP EXTENSION IF EXISTS "pgcrypto";
