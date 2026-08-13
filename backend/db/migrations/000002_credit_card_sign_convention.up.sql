-- Credit card statements typically export purchases as negative amounts and
-- payments/refunds as positive. Set the default convention so that a positive
-- amount on a credit card means a credit and a negative amount means a debit.
UPDATE account_types SET positive_txn_type = 'credit' WHERE id = 'credit_card';