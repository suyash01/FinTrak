-- H-2: loan/EMI accounts hold no transactions of their own; EMI payments live
-- on another account and are attached via loan_attachments. Handler code
-- enforces this on create and import, but PATCH could previously move an
-- existing transaction onto a loan account. This trigger backstops every write
-- path so the invariant cannot be bypassed by a future handler omission.
CREATE OR REPLACE FUNCTION enforce_transaction_not_loan_account() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM accounts a
         WHERE a.id = NEW.account_id
           AND a.account_type_id = 'loan'
    ) THEN
        RAISE EXCEPTION 'transactions cannot belong to a loan account'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS transactions_reject_loan_account ON transactions;
CREATE TRIGGER transactions_reject_loan_account
    BEFORE INSERT OR UPDATE OF account_id ON transactions
    FOR EACH ROW EXECUTE FUNCTION enforce_transaction_not_loan_account();
