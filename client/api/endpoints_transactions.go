package api

import (
	"context"
	"io"
)

// Transactions (backend/handlers/transaction.go, transaction_bulk.go,
// transaction_export.go, transaction_import.go).

// TransactionFilter is the shared query grammar of GET /transactions and
// GET /transactions/export (txnQueryFilter, backend/handlers/transaction.go).
// The zero value means "no filters".
type TransactionFilter struct {
	// AccountID narrows to one account. A single-account list also gains the
	// synthetic summary rows (per-cycle "Total outstanding" / month-end
	// "Running balance"), which arrive with IsSummary set.
	AccountID string
	// CategoryID accepts a category id, the sentinel UncategorizedCategory, or
	// a category group id/slug.
	CategoryID string
	// GroupID matches every category in a group.
	GroupID string
	// Search is a case-insensitive substring of description, notes, payee name
	// or tags.
	Search string
	// Type is "debit" or "credit".
	Type string
	// PayeeID accepts a payee id or the sentinel NoPayee.
	PayeeID string
	// Amount matches an exact amount.
	Amount   string
	DateFrom string
	DateTo   string
	// Tags is an ANY-match set (array overlap).
	Tags []string
	// Linked is a tri-state filter; nil leaves it unfiltered.
	Linked *bool
	// Uncategorized keeps only transactions without a category.
	Uncategorized bool
	// LoanAccountID filters by loan attachment.
	LoanAccountID string
	// ExcludeAttached drops transactions already attached to a loan or series.
	ExcludeAttached bool
	// RecurringID filters by recurring series; Recurring is "linked" or
	// "unlinked".
	RecurringID string
	Recurring   string

	// List-only options, ignored by the export endpoint.
	SortBy    string // date | amount | createdAt
	SortOrder string // "ASC" is exact; anything else sorts DESC
	Page      int
	Limit     int
}

// Sentinels accepted by the transaction filters.
const (
	// UncategorizedCategory clears/matches an absent category in a filter or a
	// bulk categorize.
	UncategorizedCategory = "uncategorized"
	// NoPayee matches transactions without a payee in the payeeId filter.
	NoPayee = "none"
)

// apply writes the filter parameters, skipping empty ones.
func (f TransactionFilter) apply(r *request) *request {
	r.setQuery("accountId", f.AccountID).
		setQuery("categoryId", f.CategoryID).
		setQuery("groupId", f.GroupID).
		setQuery("search", f.Search).
		setQuery("type", f.Type).
		setQuery("payeeId", f.PayeeID).
		setQuery("amount", f.Amount).
		setQuery("dateFrom", f.DateFrom).
		setQuery("dateTo", f.DateTo).
		setQuery("loanAccountId", f.LoanAccountID).
		setQuery("recurringId", f.RecurringID).
		setQuery("recurring", f.Recurring)
	setQueryList(r, "tags", f.Tags)
	setQueryPointerBool(r, "linked", f.Linked)
	if f.Uncategorized {
		r.setQueryValue("uncategorized", "true")
	}
	if f.ExcludeAttached {
		r.setQueryValue("excludeAttached", "true")
	}
	return r
}

// applyPaged adds the options only the list endpoint accepts.
func (f TransactionFilter) applyPaged(r *request) *request {
	f.apply(r)
	r.setQuery("sortBy", f.SortBy).setQuery("sortOrder", f.SortOrder).
		setQueryInt("page", f.Page).setQueryInt("limit", f.Limit)
	return r
}

// ListTransactions returns one page of transactions. Page defaults to 1 and
// limit to 50 server-side, capped at 1000.
func (c *Client) ListTransactions(ctx context.Context, f TransactionFilter) (TransactionPage, error) {
	return do[TransactionPage](ctx, c, f.applyPaged(get("/transactions")))
}

// CreateTransaction adds one transaction and returns its id. When CategoryID is
// omitted the rules engine categorizes it, and for an account with a billing
// day it is attached to the cycle covering its date.
func (c *Client) CreateTransaction(ctx context.Context, req CreateTransactionRequest) (string, error) {
	res, err := do[IDResult](ctx, c, post("/transactions").withJSON(req))
	return res.ID, err
}

// UpdateTransaction applies a partial update. Use UUIDNull() to clear
// category/payee/billing cycle.
func (c *Client) UpdateTransaction(ctx context.Context, id string, req UpdateTransactionRequest) error {
	_, err := do[MessageResult](ctx, c, patch("/transactions/"+pathEscape(id)).withJSON(req))
	return err
}

// DeleteTransaction removes one transaction.
func (c *Client) DeleteTransaction(ctx context.Context, id string) error {
	_, err := do[MessageResult](ctx, c, del("/transactions/"+pathEscape(id)))
	return err
}

// ExportTransactionsCSV streams the transactions matching f as CSV into w,
// honouring the same filters as the list but ignoring paging and sort (the
// export is always date-descending and truncated at 100 000 rows).
func (c *Client) ExportTransactionsCSV(ctx context.Context, f TransactionFilter, w io.Writer) (string, error) {
	return c.download(ctx, f.apply(get("/transactions/export")), w)
}

// ImportTransactions imports a batch. DuplicateAction is "skip" to drop rows
// that already exist (and repeats inside the batch) or "keep" to insert
// everything. PaperlessDocumentIDs are tagged with the configured paperlessTag
// once the import commits.
func (c *Client) ImportTransactions(ctx context.Context, req ImportRequest) (ImportResult, error) {
	return do[ImportResult](ctx, c, post("/transactions/import").withJSON(req))
}

// ValidateTransactions is the read-only duplicate check that mirrors the import
// endpoint's fingerprint matching.
func (c *Client) ValidateTransactions(ctx context.Context, req ValidateTransactionsRequest) (ValidateTransactionsResponse, error) {
	return do[ValidateTransactionsResponse](ctx, c, post("/transactions/validate").withJSON(req))
}

// BulkCategorize applies one category to many transactions; pass
// UncategorizedCategory to clear it. Closed accounts are skipped, so the count
// may be lower than the selection.
func (c *Client) BulkCategorize(ctx context.Context, ids []string, categoryID string) (int64, error) {
	body := BulkCategorizeRequest{TransactionIDs: ids, CategoryID: categoryID}
	res, err := do[UpdatedResult](ctx, c, post("/transactions/bulk-categorize").withJSON(body))
	return res.Updated, err
}

// BulkPayee applies one payee to many transactions.
func (c *Client) BulkPayee(ctx context.Context, ids []string, payeeID string) (int64, error) {
	body := BulkUpdatePayeeRequest{TransactionIDs: ids, PayeeID: payeeID}
	res, err := do[UpdatedResult](ctx, c, post("/transactions/bulk-payee").withJSON(body))
	return res.Updated, err
}

// BulkBillingCycle attaches many transactions to one cycle.
func (c *Client) BulkBillingCycle(ctx context.Context, ids []string, cycleID string) (int64, error) {
	body := BulkBillingCycleRequest{TransactionIDs: ids, BillingCycleID: cycleID}
	res, err := do[UpdatedResult](ctx, c, post("/transactions/bulk-billing-cycle").withJSON(body))
	return res.Updated, err
}

// BulkTags adds and/or removes tags across many transactions (remove wins when
// a tag appears in both lists).
func (c *Client) BulkTags(ctx context.Context, ids, add, remove []string) (int64, error) {
	body := BulkUpdateTagsRequest{TransactionIDs: ids, Add: add, Remove: remove}
	res, err := do[UpdatedResult](ctx, c, post("/transactions/bulk-tags").withJSON(body))
	return res.Updated, err
}

// BulkDelete deletes many transactions at once.
func (c *Client) BulkDelete(ctx context.Context, ids []string) (int64, error) {
	body := BulkDeleteTransactionsRequest{TransactionIDs: ids}
	res, err := do[DeletedResult](ctx, c, post("/transactions/bulk-delete").withJSON(body))
	return res.Deleted, err
}

// BulkAttachLoan attaches transactions to a loan/EMI account, setting each
// payee to the loan's linked payee.
func (c *Client) BulkAttachLoan(ctx context.Context, ids []string, loanAccountID string) (int64, error) {
	body := BulkLoanRequest{TransactionIDs: ids, LoanAccountID: &loanAccountID}
	res, err := do[AttachedResult](ctx, c, post("/transactions/bulk-loan").withJSON(body))
	return res.Attached, err
}

// BulkDetachLoan detaches transactions from whatever loan account they carry,
// leaving their payees unchanged.
func (c *Client) BulkDetachLoan(ctx context.Context, ids []string) (int64, error) {
	body := BulkLoanRequest{TransactionIDs: ids}
	res, err := do[DetachedResult](ctx, c, post("/transactions/bulk-loan").withJSON(body))
	return res.Detached, err
}
