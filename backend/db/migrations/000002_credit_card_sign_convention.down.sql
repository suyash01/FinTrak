-- Revert credit card sign convention to the original default (positive = debit).
UPDATE account_types SET positive_txn_type = 'debit' WHERE id = 'credit_card';