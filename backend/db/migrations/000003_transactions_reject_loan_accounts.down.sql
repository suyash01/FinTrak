DROP TRIGGER IF EXISTS transactions_reject_loan_account ON transactions;
DROP FUNCTION IF EXISTS enforce_transaction_not_loan_account();
