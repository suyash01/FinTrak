package api

import (
	"context"
	"io"
)

// Accounts, account types, billing cycles, and loan schedules
// (backend/handlers/account.go, account_type.go, billing_cycle.go, loan.go).

// ListAccounts returns every account the user owns, newest first.
func (c *Client) ListAccounts(ctx context.Context) ([]Account, error) {
	return do[[]Account](ctx, c, get("/accounts"))
}

// CreateAccount creates an account.
func (c *Client) CreateAccount(ctx context.Context, req CreateAccountRequest) (Account, error) {
	return do[Account](ctx, c, post("/accounts").withJSON(req))
}

// UpdateAccount applies a partial update. Empty strings and nil pointers mean
// "leave alone"; use IntNull() to clear the billing day.
func (c *Client) UpdateAccount(ctx context.Context, id string, req UpdateAccountRequest) (Account, error) {
	return do[Account](ctx, c, put("/accounts/"+pathEscape(id)).withJSON(req))
}

// DeleteAccount deletes an account and its transactions, reporting how many
// transactions went with it.
func (c *Client) DeleteAccount(ctx context.Context, id string) (AccountDeleteResult, error) {
	return do[AccountDeleteResult](ctx, c, del("/accounts/"+pathEscape(id)))
}

// ListBillingCycles returns an account's billing cycles, auto-generating any
// missing ones. Accounts without a billing day return an empty list.
func (c *Client) ListBillingCycles(ctx context.Context, accountID string) ([]BillingCycle, error) {
	res, err := do[DataList[BillingCycle]](ctx, c, get("/accounts/"+pathEscape(accountID)+"/billing-cycles"))
	return res.Data, err
}

// ExportAccountCSV streams one account's transactions as CSV into w and returns
// the suggested filename.
func (c *Client) ExportAccountCSV(ctx context.Context, accountID string, w io.Writer) (string, error) {
	return c.download(ctx, get("/accounts/"+pathEscape(accountID)+"/export"), w)
}

// AccountTypes is shared reference data; only admins may mutate it.

// ListAccountTypes returns the account types.
func (c *Client) ListAccountTypes(ctx context.Context) ([]AccountType, error) {
	return do[[]AccountType](ctx, c, get("/account-types"))
}

// CreateAccountType creates an account type (admin only).
func (c *Client) CreateAccountType(ctx context.Context, req CreateAccountTypeRequest) (AccountType, error) {
	return do[AccountType](ctx, c, post("/account-types").withJSON(req))
}

// UpdateAccountType edits an account type (admin only).
func (c *Client) UpdateAccountType(ctx context.Context, id string, req UpdateAccountTypeRequest) (AccountType, error) {
	return do[AccountType](ctx, c, put("/account-types/"+pathEscape(id)).withJSON(req))
}

// DeleteAccountType removes an account type (admin only). The built-in "bank",
// "credit_card", and "loan" types are refused with 403.
func (c *Client) DeleteAccountType(ctx context.Context, id string) error {
	_, err := do[MessageResult](ctx, c, del("/account-types/"+pathEscape(id)))
	return err
}

// Loan schedules.

// LoanSchedule returns a loan account's amortization table. A loan without a
// schedule answers 200 with a null schedule rather than 404.
func (c *Client) LoanSchedule(ctx context.Context, accountID string) (LoanScheduleDetail, error) {
	return do[LoanScheduleDetail](ctx, c, get("/accounts/"+pathEscape(accountID)+"/loan-schedule"))
}

// SetLoanSchedule creates or replaces a loan's terms and returns the generated
// table.
func (c *Client) SetLoanSchedule(ctx context.Context, accountID string, req LoanScheduleRequest) (LoanScheduleDetail, error) {
	return do[LoanScheduleDetail](ctx, c, put("/accounts/"+pathEscape(accountID)+"/loan-schedule").withJSON(req))
}

// DeleteLoanSchedule removes a loan's schedule (idempotent: it reports 0 when
// there was none).
func (c *Client) DeleteLoanSchedule(ctx context.Context, accountID string) (DeleteLoanScheduleResult, error) {
	return do[DeleteLoanScheduleResult](ctx, c, del("/accounts/"+pathEscape(accountID)+"/loan-schedule"))
}

// LoanPayoff quotes what settling a loan costs on a date: its outstanding
// principal plus the interest accrued from the loan's last EMI payment to that
// date. The date is required, as is a loan that has a schedule and has not
// already been settled by a transfer — anything else answers 400, which the
// caller surfaces rather than posting the transfer.
func (c *Client) LoanPayoff(ctx context.Context, accountID, date string) (LoanPayoff, error) {
	return do[LoanPayoff](ctx, c, get("/accounts/"+pathEscape(accountID)+"/loan-payoff").addQuery("date", date))
}

// TransferLoanBalance moves a loan's remaining payoff to another loan: the
// source is settled at its payoff on the transfer date — outstanding principal
// plus accrued interest, which LoanPayoff quotes — and the target is handled
// according to the request mode: recast its remaining installments, take over
// the source from its disbursement, or open a schedule when it has no terms of
// its own. A target with no schedule of its own is started from the request's
// target terms in opens mode, which the API requires in that case and ignores
// for the other modes.
func (c *Client) TransferLoanBalance(ctx context.Context, sourceAccountID string, req LoanTransferRequest) (LoanTransferResult, error) {
	return do[LoanTransferResult](ctx, c, post("/accounts/"+pathEscape(sourceAccountID)+"/loan-transfer").withJSON(req))
}

// DeleteLoanTransfer removes a recorded balance transfer, which reverts both
// loans: every recast and cancelled installment is derived from the row.
// sourceAccountID must be the transfer's source (from) account, so the delete
// cannot be aimed at the same transfer through the other side; it is idempotent
// and reports 0 when nothing matched.
func (c *Client) DeleteLoanTransfer(ctx context.Context, sourceAccountID, transferID string) (DeleteLoanTransferResult, error) {
	return do[DeleteLoanTransferResult](ctx, c, del("/accounts/"+pathEscape(sourceAccountID)+"/loan-transfer/"+pathEscape(transferID)))
}

// LinkLoanDisbursement links the bank credit that released the loan and returns
// the refreshed detail, so the caller can render the reconciliation without a
// second request. Re-linking replaces the previous credit. The transaction must
// be a credit on a non-loan account that is not already an EMI payment or
// another loan's disbursement credit.
func (c *Client) LinkLoanDisbursement(ctx context.Context, accountID, transactionID string) (LoanScheduleDetail, error) {
	req := LinkLoanDisbursementRequest{TransactionID: transactionID}
	return do[LoanScheduleDetail](ctx, c, put("/accounts/"+pathEscape(accountID)+"/loan-disbursement").withJSON(req))
}

// UnlinkLoanDisbursement removes the loan's linked credit, reporting whether one
// was there. The endpoint is idempotent, and the loan, its schedule and the
// transaction are untouched.
func (c *Client) UnlinkLoanDisbursement(ctx context.Context, accountID string) (DeleteLoanDisbursementResult, error) {
	return do[DeleteLoanDisbursementResult](ctx, c, del("/accounts/"+pathEscape(accountID)+"/loan-disbursement"))
}
