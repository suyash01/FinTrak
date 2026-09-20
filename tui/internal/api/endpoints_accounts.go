package api

import (
	"context"
	"io"
)

// Accounts, account types, billing cycles, and loan schedules
// (backend/handlers/account.go, account_type.go, billing_cycle.go, loan.go).

// ListAccounts returns every account the user owns, default first.
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

// DeleteAccountType removes an account type (admin only). The built-in "bank"
// and "credit_card" types are refused with 403.
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
