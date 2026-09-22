package api

import "time"

// This file mirrors the wire types in backend/models/models.go. The TUI is a
// separate module, so the types are transcribed rather than shared. Three
// tests in spec_parity_test.go keep the transcription honest, and between them
// they define what is guarded:
//
//   - TestRouteTableMatchesTheSpec pins the route surface in both directions:
//     every operation in backend/openapi.yaml has a compiled client call, and
//     the client claims no route the spec does not document.
//   - TestEveryRouteHitsItsDocumentedPath executes each of those calls against
//     a stub and asserts the method, the path and the complete query set it
//     produced.
//   - TestClientTypesMatchTheSpecSchemas compares the json tag of every field
//     of every struct in this file against the properties of the
//     components.schemas entry that documents it (exactly, or as a subset for
//     the request bodies the spec documents with the response schema), so a
//     mistyped or renamed tag fails the suite instead of decoding as an empty
//     field. Types the spec documents inline are listed there with the reason
//     they have no schema to compare against.
//
// What none of them check: the Go type of a field (a string tag and an integer
// schema property still compare equal), nested property shapes beyond the
// structs declared here, and the wire values themselves. UUIDs are carried as
// strings and timestamps as time.Time (the backend marshals both in their
// canonical JSON form); date-only request fields stay strings in "YYYY-MM-DD"
// form, exactly as the handlers expect.

// DateLayout is the date-only format the API accepts and returns for request
// fields, bundle records and calendar keys.
const DateLayout = "2006-01-02"

// FieldError, ErrorResponse and APIError live in errors.go.

// AccountType describes how an account category behaves; PositiveTxnType
// ("credit" or "debit") is the side added when computing a balance.
type AccountType struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	PositiveTxnType string `json:"positiveTxnType"`
}

// Account is a bank account, credit card, wallet, or loan/EMI account.
type Account struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	AccountTypeID   string    `json:"accountTypeId"`
	AccountTypeName string    `json:"accountTypeName,omitempty"`
	Bank            string    `json:"bank"`
	Currency        string    `json:"currency"`
	Color           string    `json:"color"`
	IsDefault       bool      `json:"isDefault"`
	Closed          bool      `json:"closed"`
	Balance         Amount    `json:"balance"`
	CreatedAt       time.Time `json:"createdAt"`
	// BillingDay is 1-31 and null when unset; when set, the account gets
	// billing cycles and per-cycle summary rows.
	BillingDay *int `json:"billingDay"`
}

// Payee is a merchant or counterparty. A payee linked to an account
// (AccountID) makes transfers resolve to the counterpart account's payee.
type Payee struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	AccountID *string   `json:"accountId,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// CategoryGroup is a top-level grouping for categories. The four base groups
// (income, expense, transfer, cashback) are immutable and global.
type CategoryGroup struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Icon      string  `json:"icon"`
	Color     string  `json:"color"`
	IsBase    bool    `json:"isBase"`
	IsGlobal  bool    `json:"isGlobal"`
	UserID    *string `json:"userId,omitempty"`
	SortOrder int     `json:"sortOrder"`
}

// Category buckets transactions; global categories are admin-created and shared
// with every user.
type Category struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Icon        string `json:"icon"`
	Color       string `json:"color"`
	GroupID     string `json:"groupId"`
	IsGlobal    bool   `json:"isGlobal"`
	GroupName   string `json:"groupName,omitempty"`
	GroupIsBase bool   `json:"groupIsBase,omitempty"`
}

// Transaction is one debit or credit entry. The joined fields (AccountName,
// CategoryName, …) are populated on reads, and IsSummary marks the synthetic
// non-persisted rows the API appends to a single-account list (a cycle's "Total
// outstanding" or a month-end "Running balance").
type Transaction struct {
	ID                string    `json:"id"`
	AccountID         string    `json:"accountId"`
	Date              time.Time `json:"date"`
	Description       string    `json:"description"`
	Amount            Amount    `json:"amount"`
	Type              string    `json:"type"`
	CategoryID        *string   `json:"categoryId"`
	Tags              []string  `json:"tags"`
	Notes             string    `json:"notes"`
	PayeeID           *string   `json:"payeeId,omitempty"`
	Payee             string    `json:"payee"`
	CreatedAt         time.Time `json:"createdAt"`
	AccountName       string    `json:"accountName,omitempty"`
	CategoryName      string    `json:"categoryName,omitempty"`
	CategoryIcon      string    `json:"categoryIcon,omitempty"`
	CategoryColor     string    `json:"categoryColor,omitempty"`
	IsLinked          bool      `json:"isLinked"`
	IsSummary         bool      `json:"isSummary,omitempty"`
	BillingCycleID    *string   `json:"billingCycleId,omitempty"`
	BillingCycleLabel string    `json:"billingCycleLabel,omitempty"`
	LoanAccountID     *string   `json:"loanAccountId,omitempty"`
	LoanAccountName   string    `json:"loanAccountName,omitempty"`
	RecurringSeriesID *string   `json:"recurringSeriesId,omitempty"`
	RecurringSeriesNm string    `json:"recurringSeriesName,omitempty"`
}

// TagCount is one entry of the user's tag vocabulary. Tags are not a table —
// they are derived from transactions.tags — so the name is the identity.
type TagCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// BillingCycle is a persisted statement period for an account with a billing
// day, auto-generated from that day.
type BillingCycle struct {
	ID               string    `json:"id"`
	AccountID        string    `json:"accountId"`
	StartDate        time.Time `json:"startDate"`
	EndDate          time.Time `json:"endDate"`
	Label            string    `json:"label"`
	TotalOutstanding Amount    `json:"totalOutstanding"`
	TransactionCount int       `json:"transactionCount"`
}

// Rule auto-assigns a category (and optionally a payee, tags and a note) to
// transactions matching Pattern; optional conditions are ANDed.
type Rule struct {
	ID               string   `json:"id"`
	Pattern          string   `json:"pattern"`
	MatchType        string   `json:"matchType"`
	CategoryID       string   `json:"categoryId"`
	PayeeID          *string  `json:"payeeId,omitempty"`
	Payee            string   `json:"payee"`
	Priority         int      `json:"priority"`
	AccountID        *string  `json:"accountId,omitempty"`
	FilterCategoryID *string  `json:"filterCategoryId,omitempty"`
	FilterPayeeID    *string  `json:"filterPayeeId,omitempty"`
	MinAmount        *Amount  `json:"minAmount,omitempty"`
	MaxAmount        *Amount  `json:"maxAmount,omitempty"`
	TxnType          string   `json:"txnType,omitempty"`
	DateFrom         *string  `json:"dateFrom,omitempty"`
	DateTo           *string  `json:"dateTo,omitempty"`
	IsLinked         *bool    `json:"isLinked,omitempty"`
	IsRecurring      *bool    `json:"isRecurring,omitempty"`
	AddTags          []string `json:"addTags"`
	Notes            string   `json:"notes"`
	CategoryName     string   `json:"categoryName,omitempty"`
	AccountName      string   `json:"accountName,omitempty"`
	FilterCatName    string   `json:"filterCategoryName,omitempty"`
	FilterPayeeName  string   `json:"filterPayeeName,omitempty"`
}

// RulePreview reports how many uncategorized transactions a hypothetical rule
// would categorize.
type RulePreview struct {
	Matched int `json:"matched"`
}

// Link pairs two transactions: a transfer between the user's own accounts, a
// refund/cashback against an earlier purchase, or a bill payment.
type Link struct {
	ID        string       `json:"id"`
	Type      string       `json:"type"`
	FromTxnID string       `json:"fromTxnId"`
	ToTxnID   string       `json:"toTxnId"`
	Notes     string       `json:"notes"`
	CreatedAt time.Time    `json:"createdAt"`
	FromTxn   *Transaction `json:"fromTxn,omitempty"`
	ToTxn     *Transaction `json:"toTxn,omitempty"`
}

// User is the public view of an account holder.
type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

// AdminCatalog is the admin console's view of the shared global catalog.
type AdminCatalog struct {
	Groups     []AdminCatalogGroup    `json:"groups"`
	Categories []AdminCatalogCategory `json:"categories"`
}

// AdminCatalogGroup is one global category group plus its category count.
type AdminCatalogGroup struct {
	CategoryGroup
	CategoryCount int `json:"categoryCount"`
}

// AdminCatalogCategory is one global category plus its transaction count.
type AdminCatalogCategory struct {
	Category
	TransactionCount int `json:"transactionCount"`
}

// PaperlessSettingsResponse is the safe view of the Paperless-ngx integration;
// the stored API token is never returned.
type PaperlessSettingsResponse struct {
	PaperlessURL string `json:"paperlessUrl"`
	HasToken     bool   `json:"hasToken"`
	PaperlessTag string `json:"paperlessTag"`
	PageSize     *int   `json:"pageSize"`
}

// UpdateUserSettingsRequest is a partial update: nil leaves a field alone, and
// PageSize distinguishes "not provided" from an explicit null.
type UpdateUserSettingsRequest struct {
	PaperlessURL   *string     `json:"paperlessUrl"`
	PaperlessToken *string     `json:"paperlessToken"`
	PaperlessTag   *string     `json:"paperlessTag"`
	PageSize       OptionalInt `json:"pageSize,omitzero"`
}

// PaperlessDocument is a document hosted in the user's Paperless-ngx instance.
type PaperlessDocument struct {
	ID            int      `json:"id"`
	Title         string   `json:"title"`
	Correspondent string   `json:"correspondent"`
	DocumentType  string   `json:"documentType"`
	Created       string   `json:"created"`
	Tags          []string `json:"tags"`
}

// PaperlessDocumentsResponse is the paginated document list plus the lookup
// tables the import screen renders as filters.
type PaperlessDocumentsResponse struct {
	Documents      []PaperlessDocument `json:"documents"`
	Page           int                 `json:"page"`
	PageSize       int                 `json:"pageSize"`
	TotalCount     int                 `json:"totalCount"`
	TotalPages     int                 `json:"totalPages"`
	Correspondents []string            `json:"correspondents"`
	DocumentTypes  []string            `json:"documentTypes"`
	Tags           []string            `json:"tags"`
}

// PaperlessImportRequest asks the backend to pull one Paperless document, parse
// it, and return the normalized transactions for preview.
type PaperlessImportRequest struct {
	DocumentID int    `json:"documentId"`
	Extractor  string `json:"extractor,omitempty"`
	Password   string `json:"password,omitempty"`
	DateFormat string `json:"dateFormat,omitempty"`
}

// StatementExtractor is one parser extractor offered by the parser service. The
// name is the value passed back as extractor; DisplayName is the label.
type StatementExtractor struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

// StatementParseResult is the normalized parse payload returned by both
// POST /statements/parse and POST /paperless/import (parseStatementResult in
// backend/handlers/statement.go). A non-empty ValidationErrors means the
// parser's own per-page reconciliation failed, so the rows are suspect.
type StatementParseResult struct {
	Transactions     []ImportTransaction `json:"transactions"`
	Summary          map[string]string   `json:"summary"`
	PageCount        int                 `json:"pageCount"`
	TransactionCount int                 `json:"transactionCount"`
	ValidationErrors []string            `json:"validationErrors"`
}

// ---- Accounts ----

// CreateAccountRequest is the body for POST /accounts.
type CreateAccountRequest struct {
	Name          string `json:"name"`
	AccountTypeID string `json:"accountTypeId"`
	Bank          string `json:"bank,omitempty"`
	Currency      string `json:"currency,omitempty"`
	Color         string `json:"color,omitempty"`
	IsDefault     bool   `json:"isDefault,omitempty"`
	BillingDay    *int   `json:"billingDay,omitempty"`
}

// UpdateAccountRequest is a partial update. Empty strings and nil pointers mean
// "not provided" (name and accountTypeId can never be blanked, and a nil
// BillingDay leaves the stored day alone — use IntNull to clear it).
type UpdateAccountRequest struct {
	Name          string      `json:"name,omitempty"`
	AccountTypeID string      `json:"accountTypeId,omitempty"`
	Bank          string      `json:"bank,omitempty"`
	Currency      string      `json:"currency,omitempty"`
	Color         string      `json:"color,omitempty"`
	IsDefault     *bool       `json:"isDefault,omitempty"`
	Closed        *bool       `json:"closed,omitempty"`
	BillingDay    OptionalInt `json:"billingDay,omitzero"`
}

// CreateAccountTypeRequest is the admin-only body for POST /account-types.
type CreateAccountTypeRequest struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	PositiveTxnType string `json:"positiveTxnType"`
}

// UpdateAccountTypeRequest is the admin-only body for PUT /account-types/:id.
type UpdateAccountTypeRequest struct {
	Name            string `json:"name,omitempty"`
	PositiveTxnType string `json:"positiveTxnType,omitempty"`
}

// ---- Categories and groups ----

// CreateCategoryRequest is the body for POST /categories.
type CreateCategoryRequest struct {
	Name    string `json:"name"`
	Icon    string `json:"icon,omitempty"`
	Color   string `json:"color,omitempty"`
	GroupID string `json:"groupId"`
}

// UpdateCategoryRequest is the body for PUT /categories/:id.
type UpdateCategoryRequest struct {
	Name    string `json:"name,omitempty"`
	Icon    string `json:"icon,omitempty"`
	Color   string `json:"color,omitempty"`
	GroupID string `json:"groupId,omitempty"`
}

// DeleteCategoryResult reports what deleting a category cleared.
type DeleteCategoryResult struct {
	ClearedTransactions int `json:"clearedTransactions"`
	DeletedRules        int `json:"deletedRules"`
}

// CreateCategoryGroupRequest is the body for POST /groups.
type CreateCategoryGroupRequest struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Icon  string `json:"icon,omitempty"`
	Color string `json:"color,omitempty"`
}

// UpdateCategoryGroupRequest is the body for PUT /groups/:id.
type UpdateCategoryGroupRequest struct {
	Name  string `json:"name,omitempty"`
	Icon  string `json:"icon,omitempty"`
	Color string `json:"color,omitempty"`
}

// ---- Payees ----

// CreatePayeeRequest is the body for POST /payees. AccountID links the payee to
// one of the user's accounts so transfers resolve to it.
type CreatePayeeRequest struct {
	Name      string  `json:"name"`
	AccountID *string `json:"accountId"`
}

// ---- Transactions ----

// CreateTransactionRequest is the body for POST /transactions. When CategoryID
// is omitted the transaction is auto-categorized by the rules engine, and for
// an account with a billing day it is attached to the cycle covering its date
// unless BillingCycleID overrides that.
//
// ClientKey makes the create idempotent: a request repeating a key the user has
// already used returns that transaction instead of inserting a second one, so a
// caller may retry a create whose response was lost.
type CreateTransactionRequest struct {
	AccountID      string   `json:"accountId"`
	Date           string   `json:"date"`
	Description    string   `json:"description"`
	Amount         Amount   `json:"amount"`
	Type           string   `json:"type"`
	CategoryID     *string  `json:"categoryId"`
	PayeeID        *string  `json:"payeeId"`
	Tags           []string `json:"tags,omitempty"`
	Notes          string   `json:"notes,omitempty"`
	BillingCycleID *string  `json:"billingCycleId"`
	ClientKey      string   `json:"clientKey,omitempty"`
}

// UpdateTransactionRequest is a partial update: a nil pointer leaves a field
// alone, while CategoryID/PayeeID/BillingCycleID use OptionalUUID so an
// explicit null clears the column.
type UpdateTransactionRequest struct {
	CategoryID     OptionalUUID `json:"categoryId,omitzero"`
	Tags           *[]string    `json:"tags,omitempty"`
	Notes          *string      `json:"notes,omitempty"`
	PayeeID        OptionalUUID `json:"payeeId,omitzero"`
	Date           *string      `json:"date,omitempty"`
	Description    *string      `json:"description,omitempty"`
	Amount         *Amount      `json:"amount,omitempty"`
	Type           *string      `json:"type,omitempty"`
	AccountID      *string      `json:"accountId,omitempty"`
	BillingCycleID OptionalUUID `json:"billingCycleId,omitzero"`
}

// BulkCategorizeRequest reassigns one category across many transactions.
// CategoryID also accepts the "uncategorized" sentinel, which clears it.
type BulkCategorizeRequest struct {
	TransactionIDs []string `json:"transactionIds"`
	CategoryID     string   `json:"categoryId"`
}

// BulkUpdatePayeeRequest reassigns one payee across many transactions.
type BulkUpdatePayeeRequest struct {
	TransactionIDs []string `json:"transactionIds"`
	PayeeID        string   `json:"payeeId"`
}

// BulkBillingCycleRequest attaches one billing cycle to many transactions.
type BulkBillingCycleRequest struct {
	TransactionIDs []string `json:"transactionIds"`
	BillingCycleID string   `json:"billingCycleId"`
}

// BulkUpdateTagsRequest adds and/or removes tags; Remove wins on conflict.
type BulkUpdateTagsRequest struct {
	TransactionIDs []string `json:"transactionIds"`
	Add            []string `json:"add,omitempty"`
	Remove         []string `json:"remove,omitempty"`
}

// BulkDeleteTransactionsRequest lists the transactions to delete.
type BulkDeleteTransactionsRequest struct {
	TransactionIDs []string `json:"transactionIds"`
}

// BulkLoanRequest attaches transactions to a loan account, or detaches them
// from whatever loan account they carry when LoanAccountID is nil.
type BulkLoanRequest struct {
	TransactionIDs []string `json:"transactionIds"`
	LoanAccountID  *string  `json:"loanAccountId"`
}

// RenameTagRequest renames every use of a tag across the user's transactions.
type RenameTagRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// ---- Import and validation ----

// ImportTransaction is one candidate row in an import or validation batch.
type ImportTransaction struct {
	Date        string  `json:"date"`
	Description string  `json:"description"`
	Amount      Amount  `json:"amount"`
	Type        string  `json:"type"`
	PayeeID     *string `json:"payeeId"`
}

// ImportRequest is the body for POST /transactions/import. DuplicateAction is
// "skip" or "keep"; PaperlessDocumentIDs are tagged with the configured
// paperlessTag after a successful import.
type ImportRequest struct {
	AccountID            string              `json:"accountId"`
	Transactions         []ImportTransaction `json:"transactions"`
	DuplicateAction      string              `json:"duplicateAction,omitempty"`
	BillingCycleID       *string             `json:"billingCycleId"`
	PaperlessDocumentIDs []int               `json:"paperlessDocumentIds,omitempty"`
}

// ImportResult reports what a bulk import wrote
// (transaction_import.go:279-283). Duplicates counts the rows dropped by
// duplicateAction=skip.
type ImportResult struct {
	Imported   int `json:"imported"`
	Duplicates int `json:"duplicates"`
	Total      int `json:"total"`
}

// ValidateTransactionsRequest asks whether candidate rows already exist.
type ValidateTransactionsRequest struct {
	AccountID    string              `json:"accountId"`
	Transactions []ImportTransaction `json:"transactions"`
}

// ValidateTransactionResult reports one candidate row's duplicate state; Index
// aligns with the request slice.
type ValidateTransactionResult struct {
	Index       int    `json:"index"`
	Exists      bool   `json:"exists"`
	Date        string `json:"date"`
	Description string `json:"description"`
	Amount      Amount `json:"amount"`
	Type        string `json:"type"`
}

// ValidateTransactionsResponse summarizes a validation run.
type ValidateTransactionsResponse struct {
	Total         int                         `json:"total"`
	ExistingCount int                         `json:"existingCount"`
	MissingCount  int                         `json:"missingCount"`
	Results       []ValidateTransactionResult `json:"results"`
}

// ---- Loan schedules ----

// LoanScheduleRequest is the body for PUT /accounts/:id/loan-schedule. A blank
// ProcessingFee is a loan without a fee, and a blank DisbursalDate clears one.
type LoanScheduleRequest struct {
	Principal     Amount `json:"principal"`
	ProcessingFee Amount `json:"processingFee"`
	AnnualRateBps int    `json:"annualRateBps"`
	TenureMonths  int    `json:"tenureMonths"`
	StartDate     string `json:"startDate"`
	DisbursalDate string `json:"disbursalDate,omitzero"`
}

// LoanSchedule is a loan account's amortization terms. ProcessingFee is what the
// lender charged and is reference data: the table repays the whole Principal
// whatever it was, so recording a fee never moves an installment.
// DisbursalDate is when the money was released: when it is set and the period
// from it to StartDate (the first installment date) is not a whole anchored
// month, the first installment carries day-count interest for the broken period
// and so splits differently from every later one.
type LoanSchedule struct {
	ID            string    `json:"id"`
	LoanAccountID string    `json:"loanAccountId"`
	Principal     Amount    `json:"principal"`
	ProcessingFee Amount    `json:"processingFee"`
	AnnualRateBps int       `json:"annualRateBps"`
	TenureMonths  int       `json:"tenureMonths"`
	StartDate     time.Time `json:"startDate"`
	DisbursalDate time.Time `json:"disbursalDate,omitzero"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// LoanScheduleEntry is one generated installment, split into principal and
// interest, marked Paid when an attached EMI payment covers it.
//
// The split is not uniform: the first entry differs when the disbursal-to-first-
// due period is a broken month, and every entry after a balance transfer is
// Recast (regenerated over the remaining tenure at the larger balance, so its
// amount differs from the EMI of the entries before it). Cancelled marks an
// installment voided because a transfer settled the loan, which also makes it
// ineligible to be paid.
type LoanScheduleEntry struct {
	Number        int       `json:"number"`
	DueDate       time.Time `json:"dueDate"`
	Amount        Amount    `json:"amount"`
	Principal     Amount    `json:"principal"`
	Interest      Amount    `json:"interest"`
	Balance       Amount    `json:"balance"`
	Paid          bool      `json:"paid"`
	TransactionID *string   `json:"transactionId,omitempty"`
	Recast        bool      `json:"recast"`
	Cancelled     bool      `json:"cancelled"`
}

// LoanPrincipalTransfer is one balance transfer between two loan accounts: what
// the source loan owed on TransferDate — its outstanding principal plus the
// interest accrued since its last EMI payment — moves to the target loan, whose
// installments the mode decides how to rewrite. Both sides are derived from this
// row, so deleting it reverts both.
type LoanPrincipalTransfer struct {
	ID                  string `json:"id"`
	FromLoanAccountID   string `json:"fromLoanAccountId"`
	FromLoanAccountName string `json:"fromLoanAccountName,omitempty"`
	ToLoanAccountID     string `json:"toLoanAccountId"`
	ToLoanAccountName   string `json:"toLoanAccountName,omitempty"`
	// Amount is the payoff that moved and is always Principal +
	// AccruedInterest; the two are the breakdown of it that the loan views show.
	Amount Amount `json:"amount"`
	// Principal is what the source loan had left to repay on TransferDate and
	// AccruedInterest the interest that ran from its last EMI payment to that
	// date.
	Principal       Amount    `json:"principal"`
	AccruedInterest Amount    `json:"accruedInterest"`
	TransferDate    time.Time `json:"transferDate"`
	// Mode is how the target absorbed the amount: recast (its remaining
	// installments were regenerated over the larger balance), opens (the amount
	// started a schedule the target did not have) or takeover (the amount was
	// paid out of the target's own disbursement, leaving its schedule alone).
	Mode      string    `json:"mode"`
	CreatedAt time.Time `json:"createdAt"`
}

// LoanTransferRequest is the body for POST /accounts/:id/loan-transfer, where
// :id is the source loan. The amount is never supplied by the caller: the API
// quotes the source's payoff for TransferDate — its outstanding principal plus
// the interest accrued since its last EMI payment — and that is what the balance
// transfer settles. GET /accounts/:id/loan-payoff returns the same figure
// beforehand. The target terms are read only when the target loan has no
// schedule yet (nothing to recast) and are required in that case; the two
// integers are optional so an unset one stays off the wire, and TargetStartDate
// is a date-only string the API treats "" and an absent key alike.
//
// Mode names how the target absorbs the amount. A blank Mode is the API's
// automatic choice: recast a target that has a schedule, start one for a target
// that does not. ToLoanAccountID and TransferDate are always required.
type LoanTransferRequest struct {
	ToLoanAccountID string `json:"toLoanAccountId"`
	TransferDate    string `json:"transferDate"`
	// Mode is "recast", "opens" or "takeover", or blank for the automatic
	// choice. Recast needs the target to have a schedule, opens needs it not to,
	// and takeover needs a schedule whose net disbursement covers the amount.
	Mode                string      `json:"mode,omitzero"`
	TargetAnnualRateBps OptionalInt `json:"targetAnnualRateBps,omitzero"`
	TargetTenureMonths  OptionalInt `json:"targetTenureMonths,omitzero"`
	TargetStartDate     string      `json:"targetStartDate,omitzero"`
}

// LoanTransferResult is the response of POST /accounts/:id/loan-transfer: the
// recorded transfer plus both loans' regenerated details, so the caller can
// render both sides without a second request.
type LoanTransferResult struct {
	Transfer LoanPrincipalTransfer `json:"transfer"`
	Source   LoanScheduleDetail    `json:"source"`
	Target   LoanScheduleDetail    `json:"target"`
}

// LoanPayoff is the GET /accounts/:id/loan-payoff quote for one date: what
// settling the loan then costs, i.e. its outstanding principal plus the interest
// that accrued from FromDate to AsOf. FromDate is the loan's last EMI payment, or
// — before anything has been paid — when it was disbursed, and Days is the days
// between the two. Payoff is OutstandingPrincipal + AccruedInterest: the amount a
// balance transfer on AsOf settles the loan at. The transfer endpoint computes the
// payoff the same way, so a quote and the transfer it precedes cannot disagree.
type LoanPayoff struct {
	LoanAccountName      string    `json:"loanAccountName,omitempty"`
	AsOf                 time.Time `json:"asOf"`
	FromDate             time.Time `json:"fromDate"`
	Days                 int       `json:"days"`
	OutstandingPrincipal Amount    `json:"outstandingPrincipal"`
	AccruedInterest      Amount    `json:"accruedInterest"`
	Payoff               Amount    `json:"payoff"`
}

// LoanDisbursement is what a loan actually released and how that reconciles
// against the bank credit the user linked to it. PaidOut is the amount of the
// balance transfers this loan funded as a takeover — money it released on
// another loan's behalf rather than debt it took on — so Net is the cash that
// really reached the borrower. Verified is true only when the linked credit
// equals Net exactly.
type LoanDisbursement struct {
	Sanctioned    Amount `json:"sanctioned"`
	ProcessingFee Amount `json:"processingFee"`
	PaidOut       Amount `json:"paidOut"`
	Net           Amount `json:"net"`
	// CreditTransactionID is the linked bank credit; the empty string means
	// none is linked (a transaction id is never blank), which is also when
	// CreditAmount and Difference stay zero.
	CreditTransactionID string `json:"creditTransactionId,omitempty"`
	CreditAmount        Amount `json:"creditAmount,omitempty"`
	Verified            bool   `json:"verified"`
	Difference          Amount `json:"difference"`
}

// LoanScheduleDetail is the GET /accounts/:id/loan-schedule response. Schedule
// is null (with an empty table and zeroed totals) when the loan has none, and
// Disbursement is absent for the same reason: there is nothing to reconcile
// without terms.
type LoanScheduleDetail struct {
	Schedule        *LoanSchedule     `json:"schedule"`
	LoanAccountName string            `json:"loanAccountName,omitempty"`
	Disbursement    *LoanDisbursement `json:"disbursement,omitzero"`
	// EMI is the installment in force: the amount of the current table segment,
	// which after a balance transfer is the recast one. Individual entries still
	// carry their own Amount, since a stub first period and every recast segment
	// differ from it.
	EMI           Amount              `json:"emi"`
	TotalInterest Amount              `json:"totalInterest"`
	TotalPayable  Amount              `json:"totalPayable"`
	Entries       []LoanScheduleEntry `json:"entries"`
	// Transfers touching this loan, in date order, whether it gave or received
	// the balance. SettledOn is set when one of them settled this loan (it gave
	// the balance away), which cancels every later installment and zeroes the
	// outstanding principal.
	Transfers []LoanPrincipalTransfer `json:"transfers"`
	SettledOn *time.Time              `json:"settledOn,omitempty"`
	// LastPaidDate is the date of the payment covering the highest installment
	// paid so far; a payoff quote accrues settlement interest from it, or from the
	// disbursal date until something has been paid. It is absent when nothing has
	// been paid and there is no disbursal date.
	LastPaidDate         *time.Time `json:"lastPaidDate,omitempty"`
	PaidInstallments     int        `json:"paidInstallments"`
	PaidAmount           Amount     `json:"paidAmount"`
	PrincipalPaid        Amount     `json:"principalPaid"`
	InterestPaid         Amount     `json:"interestPaid"`
	OutstandingPrincipal Amount     `json:"outstandingPrincipal"`
	NextDueDate          *time.Time `json:"nextDueDate,omitempty"`
	Completed            bool       `json:"completed"`
}

// DeleteLoanScheduleResult reports how many schedules a delete removed.
type DeleteLoanScheduleResult struct {
	Deleted int64 `json:"deleted"`
}

// DeleteLoanTransferResult reports how many balance transfers a delete reverted
// (0 or 1).
type DeleteLoanTransferResult struct {
	Deleted int64 `json:"deleted"`
}

// LinkLoanDisbursementRequest is the body for PUT /accounts/:id/loan-disbursement:
// the bank credit that released the loan. Re-linking replaces the previous one.
type LinkLoanDisbursementRequest struct {
	TransactionID string `json:"transactionId"`
}

// DeleteLoanDisbursementResult reports whether unlinking removed a credit (0
// when none was linked, so the endpoint is idempotent).
type DeleteLoanDisbursementResult struct {
	Deleted int64 `json:"deleted"`
}

// ---- Rules ----

// CreateRuleRequest is the body for POST /rules.
type CreateRuleRequest struct {
	Pattern          string   `json:"pattern"`
	MatchType        string   `json:"matchType,omitempty"`
	CategoryID       string   `json:"categoryId"`
	PayeeID          *string  `json:"payeeId"`
	Priority         int      `json:"priority,omitempty"`
	AccountID        *string  `json:"accountId"`
	FilterCategoryID *string  `json:"filterCategoryId"`
	FilterPayeeID    *string  `json:"filterPayeeId"`
	MinAmount        *Amount  `json:"minAmount"`
	MaxAmount        *Amount  `json:"maxAmount"`
	TxnType          string   `json:"txnType,omitempty"`
	DateFrom         string   `json:"dateFrom,omitempty"`
	DateTo           string   `json:"dateTo,omitempty"`
	IsLinked         *bool    `json:"isLinked"`
	IsRecurring      *bool    `json:"isRecurring"`
	AddTags          []string `json:"addTags,omitempty"`
	Notes            string   `json:"notes,omitempty"`
}

// UpdateRuleRequest is the body for PUT /rules/:id.
type UpdateRuleRequest = CreateRuleRequest

// ---- Links ----

// CreateLinkRequest pairs two transactions. Type is transfer, cashback, refund
// or bill_payment.
type CreateLinkRequest struct {
	Type      string `json:"type"`
	FromTxnID string `json:"fromTxnId"`
	ToTxnID   string `json:"toTxnId"`
	Notes     string `json:"notes,omitempty"`
}

// BulkCreateLinksRequest creates many links in one call.
type BulkCreateLinksRequest struct {
	Links []CreateLinkRequest `json:"links"`
}

// BulkDeleteLinksRequest deletes many links in one call.
type BulkDeleteLinksRequest struct {
	IDs []string `json:"ids"`
}

// TransferSuggestion proposes a transfer link between a debit and a credit.
type TransferSuggestion struct {
	DebitTxn  Transaction `json:"debitTxn"`
	CreditTxn Transaction `json:"creditTxn"`
	Score     float64     `json:"score"`
}

// ---- Dashboard ----

// DashboardSummary is the dashboard aggregate. In billing-cycle view
// BillingCycleTrend replaces MonthlyTrend and CurrentCycle describes the
// in-progress period.
type DashboardSummary struct {
	TotalAccounts      int                     `json:"totalAccounts"`
	TotalTransactions  int                     `json:"totalTransactions"`
	TotalIncome        Amount                  `json:"totalIncome"`
	TotalExpense       Amount                  `json:"totalExpense"`
	ByCategory         []CategorySpend         `json:"byCategory"`
	IncomeByCategory   []CategorySpend         `json:"incomeByCategory"`
	MonthlyTrend       []MonthlyData           `json:"monthlyTrend"`
	RecentTransactions []Transaction           `json:"recentTransactions"`
	CurrentCycle       *CurrentCycleInfo       `json:"currentCycle,omitempty"`
	BillingCycleTrend  []BillingCycleTrendItem `json:"billingCycleTrend,omitempty"`
}

// CurrentCycleInfo describes the billing cycle currently in progress.
type CurrentCycleInfo struct {
	ID        string    `json:"id"`
	StartDate time.Time `json:"startDate"`
	EndDate   time.Time `json:"endDate"`
	Label     string    `json:"label"`
}

// BillingCycleTrendItem holds one cycle's income and expense totals.
type BillingCycleTrendItem struct {
	Label     string    `json:"label"`
	StartDate time.Time `json:"startDate"`
	EndDate   time.Time `json:"endDate"`
	Income    Amount    `json:"income"`
	Expense   Amount    `json:"expense"`
}

// CategorySpend aggregates one category's total and transaction count.
type CategorySpend struct {
	CategoryID    string `json:"categoryId"`
	CategoryName  string `json:"categoryName"`
	CategoryColor string `json:"categoryColor"`
	CategoryIcon  string `json:"categoryIcon"`
	Total         Amount `json:"total"`
	Count         int    `json:"count"`
}

// MonthlyData holds one month's totals, keyed "YYYY-MM".
type MonthlyData struct {
	Month   string `json:"month"`
	Income  Amount `json:"income"`
	Expense Amount `json:"expense"`
}

// ---- Money flow ----

// MoneyFlowNode is one node of the money-flow graph. Kind is income, account,
// category or payee; IDs are stage-prefixed (e.g. "account:<uuid>").
type MoneyFlowNode struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Color string `json:"color,omitempty"`
	Group string `json:"group,omitempty"`
	Total Amount `json:"total"`
}

// MoneyFlowEdge is one aggregated flow between two node IDs.
type MoneyFlowEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Value  Amount `json:"value"`
}

// MoneyFlowLinkSummary is a per-link-type rollup for the graph's window.
type MoneyFlowLinkSummary struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
	Total Amount `json:"total"`
}

// MoneyFlowGraph is the GET /dashboard/money-flow response: an acyclic graph
// plus the link-type summary that is not drawn as edges.
type MoneyFlowGraph struct {
	Nodes        []MoneyFlowNode        `json:"nodes"`
	Links        []MoneyFlowEdge        `json:"links"`
	TotalIncome  Amount                 `json:"totalIncome"`
	TotalExpense Amount                 `json:"totalExpense"`
	LinkSummary  []MoneyFlowLinkSummary `json:"linkSummary"`
}

// MoneyFlowTimelinePeriod is one period of the flow timeline; the bounds are
// inclusive and can be fed straight back into the graph query.
type MoneyFlowTimelinePeriod struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
	Income    Amount `json:"income"`
	Expense   Amount `json:"expense"`
	Net       Amount `json:"net"`
}

// MoneyFlowTimeline is the GET /dashboard/money-flow/timeline response.
type MoneyFlowTimeline struct {
	GroupBy string                    `json:"groupBy"`
	Periods []MoneyFlowTimelinePeriod `json:"periods"`
}

// ---- Circular money ----

// LinkCycleAccount is one participant of a circular flow.
type LinkCycleAccount struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

// LinkFlowTypeTotal is a per-link-type rollup of an account-to-account flow.
type LinkFlowTypeTotal struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
	Total Amount `json:"total"`
}

// LinkCycleLeg is one directed flow inside a cycle.
type LinkCycleLeg struct {
	FromAccountID    string              `json:"fromAccountId"`
	FromAccountName  string              `json:"fromAccountName"`
	FromAccountColor string              `json:"fromAccountColor,omitempty"`
	ToAccountID      string              `json:"toAccountId"`
	ToAccountName    string              `json:"toAccountName"`
	ToAccountColor   string              `json:"toAccountColor,omitempty"`
	Amount           Amount              `json:"amount"`
	Count            int                 `json:"count"`
	Types            []LinkFlowTypeTotal `json:"types"`
}

// LinkCycle is one circular money flow. Kind is "reciprocal" (a pair flowing
// both ways, netted for the Sankey) or "cycle" (a longer loop broken by
// dropping its back edge); Net is the smallest leg, i.e. what actually
// circulates.
type LinkCycle struct {
	Kind         string             `json:"kind"`
	Accounts     []LinkCycleAccount `json:"accounts"`
	Legs         []LinkCycleLeg     `json:"legs"`
	Net          Amount             `json:"net"`
	Gross        Amount             `json:"gross"`
	Transactions int                `json:"transactions"`
}

// LinkOneSidedFlow is a directed account flow with no counterpart, i.e. a
// possibly half-entered transfer.
type LinkOneSidedFlow struct {
	FromAccountID    string              `json:"fromAccountId"`
	FromAccountName  string              `json:"fromAccountName"`
	FromAccountColor string              `json:"fromAccountColor,omitempty"`
	ToAccountID      string              `json:"toAccountId"`
	ToAccountName    string              `json:"toAccountName"`
	ToAccountColor   string              `json:"toAccountColor,omitempty"`
	Total            Amount              `json:"total"`
	Count            int                 `json:"count"`
	Types            []LinkFlowTypeTotal `json:"types"`
}

// LinkCycleReport is the GET /links/cycles response.
type LinkCycleReport struct {
	Cycles        []LinkCycle        `json:"cycles"`
	TotalCircular Amount             `json:"totalCircular"`
	OneSidedFlows []LinkOneSidedFlow `json:"oneSidedFlows"`
}

// ---- Cash-flow calendar ----

// CashFlowCalendarDay is one day of daily net flow; days without transactions
// are omitted and filled in by the UI.
type CashFlowCalendarDay struct {
	Date    string `json:"date"`
	Income  Amount `json:"income"`
	Expense Amount `json:"expense"`
	Net     Amount `json:"net"`
	Count   int    `json:"count"`
}

// CashFlowCalendarMarker is a synthetic summary point (a month-end running
// balance or a cycle's total outstanding).
type CashFlowCalendarMarker struct {
	Date   string `json:"date"`
	Label  string `json:"label"`
	Kind   string `json:"kind"`
	Amount Amount `json:"amount"`
}

// CashFlowCalendarCycle is a billing-cycle boundary so the calendar can mark
// statement periods.
type CashFlowCalendarCycle struct {
	ID          string    `json:"id"`
	Label       string    `json:"label"`
	StartDate   time.Time `json:"startDate"`
	EndDate     time.Time `json:"endDate"`
	Outstanding Amount    `json:"outstanding"`
}

// CashFlowCalendar is the GET /dashboard/cash-flow-calendar response.
type CashFlowCalendar struct {
	Days         []CashFlowCalendarDay    `json:"days"`
	Markers      []CashFlowCalendarMarker `json:"markers"`
	Cycles       []CashFlowCalendarCycle  `json:"cycles"`
	TotalIncome  Amount                   `json:"totalIncome"`
	TotalExpense Amount                   `json:"totalExpense"`
	Net          Amount                   `json:"net"`
	MaxAbsNet    Amount                   `json:"maxAbsNet"`
}

// ---- Recurring series ----

// RecurringSeries is a repeating charge or income template. It never creates
// transactions on its own; the forecast projects it and suggestions propose
// links the user confirms.
type RecurringSeries struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Description   string     `json:"description"`
	Type          string     `json:"type"`
	Frequency     string     `json:"frequency"`
	Interval      int        `json:"interval"`
	CategoryID    *string    `json:"categoryId,omitempty"`
	PayeeID       *string    `json:"payeeId,omitempty"`
	Active        bool       `json:"active"`
	Notes         string     `json:"notes"`
	CreatedAt     time.Time  `json:"createdAt"`
	AccountID     string     `json:"accountId"`
	Amount        Amount     `json:"amount"`
	StartDate     time.Time  `json:"startDate"`
	EndDate       *time.Time `json:"endDate,omitempty"`
	AccountName   string     `json:"accountName,omitempty"`
	CategoryName  string     `json:"categoryName,omitempty"`
	CategoryIcon  string     `json:"categoryIcon,omitempty"`
	CategoryColor string     `json:"categoryColor,omitempty"`
	Payee         string     `json:"payee,omitempty"`
	NextDueDate   *time.Time `json:"nextDueDate,omitempty"`
	MonthlyAmount Amount     `json:"monthlyAmount"`
	AttachedCount int        `json:"attachedCount"`
}

// RecurringForecastItem is one projected occurrence; Matched means an attached
// transaction already covers it.
type RecurringForecastItem struct {
	Date    time.Time `json:"date"`
	Amount  Amount    `json:"amount"`
	Type    string    `json:"type"`
	Matched bool      `json:"matched"`
}

// RecurringSuggestion proposes that a transaction satisfies one occurrence.
type RecurringSuggestion struct {
	Txn            Transaction `json:"txn"`
	Score          float64     `json:"score"`
	OccurrenceDate time.Time   `json:"occurrenceDate"`
	DaysOff        float64     `json:"daysOff"`
}

// RecurringSeriesRange is one date-ranged amount/account entry. The end date is
// exclusive, so adjacent entries may share a boundary.
type RecurringSeriesRange struct {
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate,omitempty"`
	Amount    Amount `json:"amount"`
	AccountID string `json:"accountId"`
}

// CreateRecurringSeriesRequest supplies either a full Ranges list or a single
// account/amount/start date.
type CreateRecurringSeriesRequest struct {
	AccountID   *string                `json:"accountId"`
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Amount      *Amount                `json:"amount"`
	Type        string                 `json:"type"`
	Frequency   string                 `json:"frequency"`
	Interval    int                    `json:"interval,omitempty"`
	StartDate   string                 `json:"startDate,omitempty"`
	EndDate     string                 `json:"endDate,omitempty"`
	CategoryID  *string                `json:"categoryId"`
	PayeeID     *string                `json:"payeeId"`
	Active      *bool                  `json:"active"`
	Notes       string                 `json:"notes,omitempty"`
	Ranges      []RecurringSeriesRange `json:"ranges,omitempty"`
}

// UpdateRecurringSeriesRequest is a partial update. When Ranges is non-empty
// the whole range list is replaced; otherwise an Amount/AccountID change is
// recorded as a new range starting at EffectiveDate.
type UpdateRecurringSeriesRequest struct {
	AccountID     *string                `json:"accountId,omitempty"`
	Name          *string                `json:"name,omitempty"`
	Description   *string                `json:"description,omitempty"`
	Amount        *Amount                `json:"amount,omitempty"`
	Type          *string                `json:"type,omitempty"`
	Frequency     *string                `json:"frequency,omitempty"`
	Interval      *int                   `json:"interval,omitempty"`
	CategoryID    OptionalUUID           `json:"categoryId,omitzero"`
	PayeeID       OptionalUUID           `json:"payeeId,omitzero"`
	Active        *bool                  `json:"active,omitempty"`
	Notes         *string                `json:"notes,omitempty"`
	EffectiveDate *string                `json:"effectiveDate,omitempty"`
	Ranges        []RecurringSeriesRange `json:"ranges,omitempty"`
}

// RecurringSeriesTerm is one date-ranged amount/account segment of a series.
type RecurringSeriesTerm struct {
	ID          string     `json:"id"`
	SeriesID    string     `json:"seriesId"`
	StartDate   time.Time  `json:"startDate"`
	EndDate     *time.Time `json:"endDate,omitempty"`
	Amount      Amount     `json:"amount"`
	AccountID   string     `json:"accountId"`
	AccountName string     `json:"accountName,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
}

// CreateRecurringSeriesTermRequest is the body for PUT /recurring/:id/terms.
type CreateRecurringSeriesTermRequest struct {
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate,omitempty"`
	Amount    Amount `json:"amount"`
	AccountID string `json:"accountId"`
}

// UpdateRecurringSeriesTermRequest is the body for PUT /recurring/:id/terms/:termId;
// EndDate "" clears the range end.
type UpdateRecurringSeriesTermRequest struct {
	StartDate *string `json:"startDate,omitempty"`
	EndDate   *string `json:"endDate,omitempty"`
	Amount    *Amount `json:"amount,omitempty"`
	AccountID *string `json:"accountId,omitempty"`
}

// RecurringAttachRequest links transactions to one series; a transaction may
// belong to at most one series.
type RecurringAttachRequest struct {
	SeriesID       string   `json:"seriesId"`
	TransactionIDs []string `json:"transactionIds"`
}

// RecurringDetachRequest unlinks transactions from whatever series they carry.
type RecurringDetachRequest struct {
	TransactionIDs []string `json:"transactionIds"`
}

// ---- Response wrappers ----
//
// Most endpoints answer with a bare struct or slice, but a minority wrap the
// payload: {"data":[…]} for the unpaginated lists, {"data":…,"page":…} for the
// paginated ones, and a one-key counter object for the bulk operations. The
// exact key per route is pinned by the handler report in spec_parity_test.go's
// route table.

// DataList is the {"data":[…]} response of the unpaginated list endpoints
// (billing cycles, tags, recurring series and their sub-resources).
type DataList[T any] struct {
	Data []T `json:"data"`
}

// TransactionPage is the paginated GET /transactions response.
type TransactionPage struct {
	Data  []Transaction `json:"data"`
	Total int           `json:"total"`
	Page  int           `json:"page"`
	Limit int           `json:"limit"`
	Pages int           `json:"pages"`
}

// SuggestionPage is the paginated suggestion response. Unlike the transaction
// list it carries no total, only HasMore.
type SuggestionPage struct {
	Data    []TransferSuggestion `json:"data"`
	Page    int                  `json:"page"`
	Limit   int                  `json:"limit"`
	HasMore bool                 `json:"hasMore"`
}

// ExtractorsResponse is the {"extractors":[…]} response of
// GET /statements/extractors.
type ExtractorsResponse struct {
	Extractors []StatementExtractor `json:"extractors"`
}

// HealthResult is the {"status":"ok"} response of GET /health.
type HealthResult struct {
	Status string `json:"status"`
}

// The one-key counter acknowledgements. Each bulk route answers with exactly
// one of these, so they are distinct types rather than one multi-field struct.

// UpdatedResult is {"updated":n} — bulk categorize/payee/billing-cycle/tags,
// tag rename, and POST /rules/apply.
type UpdatedResult struct {
	Updated int64 `json:"updated"`
}

// DeletedResult is {"deleted":n} — bulk delete.
type DeletedResult struct {
	Deleted int64 `json:"deleted"`
}

// AttachedResult is {"attached":n} — bulk loan attach, recurring attach.
type AttachedResult struct {
	Attached int64 `json:"attached"`
}

// DetachedResult is {"detached":n} — bulk loan detach, recurring detach.
type DetachedResult struct {
	Detached int64 `json:"detached"`
}

// MessageResult is the generic {"message":…} acknowledgement returned by the
// DELETE endpoints, logout, and refresh.
type MessageResult struct {
	Message string `json:"message"`
}

// CreatedCountResult is {"createdCount":n} — POST /links/bulk.
type CreatedCountResult struct {
	CreatedCount int `json:"createdCount"`
}

// DeletedCountResult is {"message":…,"deletedCount":n} — POST /links/bulk-delete.
type DeletedCountResult struct {
	Message      string `json:"message"`
	DeletedCount int    `json:"deletedCount"`
}

// AccountDeleteResult is {"message":…,"transactionsDeleted":n}.
type AccountDeleteResult struct {
	Message             string `json:"message"`
	TransactionsDeleted int64  `json:"transactionsDeleted"`
}

// IDResult is the {"id":…} response of POST /transactions.
type IDResult struct {
	ID string `json:"id"`
}

// BackupImportResult reports how many rows a restore created and any rows it
// skipped. It mirrors models.BackupImportResult field for field: the loan
// balance transfers and disbursements a bundle carried are counted too, so the
// restore summary can account for every resource the export writes.
type BackupImportResult struct {
	Accounts             int      `json:"accounts"`
	CategoryGroups       int      `json:"categoryGroups"`
	Categories           int      `json:"categories"`
	Payees               int      `json:"payees"`
	BillingCycles        int      `json:"billingCycles"`
	Transactions         int      `json:"transactions"`
	Links                int      `json:"links"`
	LoanAttachments      int      `json:"loanAttachments"`
	LoanSchedules        int      `json:"loanSchedules"`
	LoanTransfers        int      `json:"loanTransfers"`
	LoanDisbursements    int      `json:"loanDisbursements"`
	RecurringSeries      int      `json:"recurringSeries"`
	RecurringTerms       int      `json:"recurringTerms"`
	RecurringAttachments int      `json:"recurringAttachments"`
	Rules                int      `json:"rules"`
	Warnings             []string `json:"warnings,omitempty"`
}
