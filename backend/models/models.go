// Package models defines the shared request, response, and persistence types
// used across the FinTrak API. JSON tags mirror the shapes the frontend expects,
// and optional-field types (OptionalUUID, OptionalInt) encode the difference
// between "key absent" and "key explicitly null" on partial updates.
package models

import (
	"encoding/json"
	"time"

	"github.com/fintrak/backend/internal/money"
	"github.com/google/uuid"
)

// FieldError describes a single invalid field in a request body.
type FieldError struct {
	Field   string `json:"field,omitempty"`
	Tag     string `json:"tag,omitempty"`
	Message string `json:"message"`
}

// ErrorResponse is the standard error envelope returned by all API handlers.
type ErrorResponse struct {
	Errors []FieldError `json:"errors"`
}

// AccountType describes how an account category behaves. PositiveTxnType
// ("credit" or "debit") defines which transaction type is added when computing
// an account's balance/outstanding. Types are shared reference data across all
// users; the built-in "bank", "credit_card", and "loan" types are immutable.
type AccountType struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	PositiveTxnType string `json:"positiveTxnType"`
}

// Account is a user's financial account, such as a bank account, credit card,
// wallet, or loan/EMI account.
type Account struct {
	ID              uuid.UUID `json:"id"`
	Name            string    `json:"name"`
	AccountTypeID   string    `json:"accountTypeId"`
	AccountTypeName string    `json:"accountTypeName,omitempty"`
	Bank            string    `json:"bank"`
	Currency        string    `json:"currency"`
	Color           string    `json:"color"`
	IsDefault       bool      `json:"isDefault"`
	// Closed marks an account as closed: transactions can no longer be added,
	// removed, or edited on it (linking remains possible).
	Closed    bool         `json:"closed"`
	Balance   money.Amount `json:"balance"`
	CreatedAt time.Time    `json:"createdAt"`
	// BillingDay is the day of the month on which billing cycles end (1-31,
	// clamped to the month length). It is optional; when set, per-cycle summary
	// rows are shown for the account regardless of its type.
	BillingDay *int `json:"billingDay"`
}

// Payee is a merchant or party a transaction is associated with. Payees can be
// linked to an account (AccountID) so transfers between the user's own accounts
// resolve to the counterpart account's payee.
type Payee struct {
	ID        uuid.UUID  `json:"id"`
	Name      string     `json:"name"`
	AccountID *uuid.UUID `json:"accountId,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

// CategoryGroup is a top-level grouping for categories. The four base groups
// (income, expense, transfer, cashback) are immutable and shared globally
// (UserID nil); users can add their own custom groups (UserID set). IsBase marks
// the built-in, non-deletable groups.
type CategoryGroup struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Icon      string     `json:"icon"`
	Color     string     `json:"color"`
	IsBase    bool       `json:"isBase"`
	IsGlobal  bool       `json:"isGlobal"`
	UserID    *uuid.UUID `json:"userId,omitempty"`
	SortOrder int        `json:"sortOrder"`
}

// Category is a user-scoped (or global) grouping for transactions. GroupID
// references a CategoryGroup ("income", "expense", "transfer", "cashback", or a
// user's custom group).
type Category struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Icon     string    `json:"icon"`
	Color    string    `json:"color"`
	GroupID  string    `json:"groupId"`
	IsGlobal bool      `json:"isGlobal"`
	// Joined
	GroupName   string `json:"groupName,omitempty"`
	GroupIsBase bool   `json:"groupIsBase,omitempty"`
}

// Transaction is a single debit or credit entry on an account. The joined
// fields (AccountName, CategoryName, ...) are populated by read queries and are
// absent on writes.
type Transaction struct {
	ID          uuid.UUID    `json:"id"`
	AccountID   uuid.UUID    `json:"accountId"`
	Date        time.Time    `json:"date"`
	Description string       `json:"description"`
	Amount      money.Amount `json:"amount"`
	Type        string       `json:"type"`
	CategoryID  *uuid.UUID   `json:"categoryId"`
	Tags        []string     `json:"tags"`
	Notes       string       `json:"notes"`
	PayeeID     *uuid.UUID   `json:"payeeId,omitempty"`
	Payee       string       `json:"payee"`
	CreatedAt   time.Time    `json:"createdAt"`
	// Joined fields
	AccountName   string `json:"accountName,omitempty"`
	CategoryName  string `json:"categoryName,omitempty"`
	CategoryIcon  string `json:"categoryIcon,omitempty"`
	CategoryColor string `json:"categoryColor,omitempty"`
	IsLinked      bool   `json:"isLinked"`
	IsSummary     bool   `json:"isSummary,omitempty"`
	// Billing cycle attachment for an account with a configured billing day.
	BillingCycleID    *uuid.UUID `json:"billingCycleId,omitempty"`
	BillingCycleLabel string     `json:"billingCycleLabel,omitempty"`
	// Loan/EMI attachment: the loan account this transaction is linked to as
	// an EMI payment (via loan_attachments). A transaction can be attached to
	// at most one loan account.
	LoanAccountID   *uuid.UUID `json:"loanAccountId,omitempty"`
	LoanAccountName string     `json:"loanAccountName,omitempty"`
	// Recurring subscription attachment: the recurring series this transaction
	// is linked to (via recurring_attachments). At most one series.
	RecurringSeriesID   *uuid.UUID `json:"recurringSeriesId,omitempty"`
	RecurringSeriesName string     `json:"recurringSeriesName,omitempty"`
}

// TagCount is one entry of the user's tag vocabulary: the distinct tag name
// and how many transactions carry it. Tags are not a table — they live in
// transactions.tags — so this is an on-the-fly aggregation used to power the
// tag picker, filter, and management UI.
type TagCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// AdminCatalog is the admin console's view of the shared global catalog: the
// global (user_id NULL) category groups and categories with usage counts, so an
// operator can see what is actually referenced before retiring or retitling it.
type AdminCatalog struct {
	Groups     []AdminCatalogGroup    `json:"groups"`
	Categories []AdminCatalogCategory `json:"categories"`
}

// AdminCatalogGroup is one global category group plus the number of global and
// user-owned categories that belong to it.
type AdminCatalogGroup struct {
	CategoryGroup
	CategoryCount int `json:"categoryCount"`
}

// AdminCatalogCategory is one global category plus how many transactions
// reference it (across every user).
type AdminCatalogCategory struct {
	Category
	TransactionCount int `json:"transactionCount"`
}

// BillingCycle is a persisted billing period for an account with a billing day
// set. Cycles are auto-generated from the account's billing day; transactions
// are attached to them via Transaction.BillingCycleID. TotalOutstanding is the
// account's running balance at the cycle's end date — all debits minus all
// credits (purchases net of payments, refunds, and cashbacks) posted up to
// that date.
type BillingCycle struct {
	ID               uuid.UUID    `json:"id"`
	AccountID        uuid.UUID    `json:"accountId"`
	StartDate        time.Time    `json:"startDate"`
	EndDate          time.Time    `json:"endDate"`
	Label            string       `json:"label"`
	TotalOutstanding money.Amount `json:"totalOutstanding"`
	TransactionCount int          `json:"transactionCount"`
}

// Rule automatically assigns a category (and optionally a payee) to a
// transaction whose description matches Pattern. Rules are evaluated in
// priority order (highest first) during transaction creation, imports, and
// ApplyRules.
//
// On top of the description pattern a rule may carry optional ANDed conditions
// (account, category, payee, amount range, transaction type, date window, and
// linked/recurring state) and optional extra actions (add tags, append a note).
// Every condition is a pointer/empty value when unset, so a rule with no
// conditions behaves exactly like the original pattern-only rule.
type Rule struct {
	ID         uuid.UUID  `json:"id"`
	Pattern    string     `json:"pattern"`
	MatchType  string     `json:"matchType"`
	CategoryID uuid.UUID  `json:"categoryId"`
	PayeeID    *uuid.UUID `json:"payeeId,omitempty"`
	Payee      string     `json:"payee"`
	Priority   int        `json:"priority"`

	// Conditions (all optional; when set they must all match).
	AccountID        *uuid.UUID    `json:"accountId,omitempty"`
	FilterCategoryID *uuid.UUID    `json:"filterCategoryId,omitempty"`
	FilterPayeeID    *uuid.UUID    `json:"filterPayeeId,omitempty"`
	MinAmount        *money.Amount `json:"minAmount,omitempty"`
	MaxAmount        *money.Amount `json:"maxAmount,omitempty"`
	TxnType          string        `json:"txnType,omitempty"`
	DateFrom         *string       `json:"dateFrom,omitempty"`
	DateTo           *string       `json:"dateTo,omitempty"`
	IsLinked         *bool         `json:"isLinked,omitempty"`
	IsRecurring      *bool         `json:"isRecurring,omitempty"`

	// Actions (applied on top of the category/payee assignment).
	AddTags []string `json:"addTags"`
	Notes   string   `json:"notes"`

	// Joined
	CategoryName       string `json:"categoryName,omitempty"`
	AccountName        string `json:"accountName,omitempty"`
	FilterCategoryName string `json:"filterCategoryName,omitempty"`
	FilterPayeeName    string `json:"filterPayeeName,omitempty"`
}

// RulePreview reports how many currently-uncategorized transactions a
// hypothetical rule (or rule edit) would categorize. It powers the rule
// editor's live "N transactions match" hint without writing anything; it does
// not describe which individual actions would be applied.
type RulePreview struct {
	Matched int `json:"matched"`
}

// Link pairs two transactions that belong together — typically a transfer
// between the user's own accounts, a cashback/refund that corresponds to an
// earlier purchase, or a bill payment. FromTxn/ToTxn carry the joined
// transaction details.
type Link struct {
	ID        uuid.UUID `json:"id"`
	Type      string    `json:"type"`
	FromTxnID uuid.UUID `json:"fromTxnId"`
	ToTxnID   uuid.UUID `json:"toTxnId"`
	Notes     string    `json:"notes"`
	CreatedAt time.Time `json:"createdAt"`
	// Joined
	FromTxn *Transaction `json:"fromTxn,omitempty"`
	ToTxn   *Transaction `json:"toTxn,omitempty"`
}

// Request/Response types

// User is the public view of an account (never includes the password hash).
type User struct {
	ID    uuid.UUID `json:"id"`
	Email string    `json:"email"`
	Role  string    `json:"role"`
}

// UserSettings holds per-user integration configuration, stored against the
// user row rather than in docker/env config.
type UserSettings struct {
	PaperlessURL   string `json:"paperlessUrl"`
	PaperlessToken string `json:"paperlessToken"`
	PaperlessTag   string `json:"paperlessTag"`
	PageSize       *int   `json:"pageSize"`
}

// PaperlessSettingsResponse is the safe view of a user's Paperless-ngx
// integration. It never contains the API token — callers learn whether a token
// is configured via HasToken and supply a replacement explicitly on save.
type PaperlessSettingsResponse struct {
	PaperlessURL string `json:"paperlessUrl"`
	HasToken     bool   `json:"hasToken"`
	PaperlessTag string `json:"paperlessTag"`
	PageSize     *int   `json:"pageSize"`
}

// UpdateUserSettingsRequest is a partial update for a user's integration
// settings. Pointer fields distinguish "not provided" from "set to empty", and
// PageSize uses OptionalInt so an explicit null clears the stored value.
type UpdateUserSettingsRequest struct {
	PaperlessURL   *string     `json:"paperlessUrl"`
	PaperlessToken *string     `json:"paperlessToken"`
	PaperlessTag   *string     `json:"paperlessTag"`
	PageSize       OptionalInt `json:"pageSize"`
}

// PaperlessDocument is a lightweight summary of a document hosted in a
// Paperless-ngx instance, returned by the paperless list endpoint.
type PaperlessDocument struct {
	ID            int      `json:"id"`
	Title         string   `json:"title"`
	Correspondent string   `json:"correspondent"`
	DocumentType  string   `json:"documentType"`
	Created       string   `json:"created"`
	Tags          []string `json:"tags"`
}

// PaperlessDocumentsResponse is the paginated document list returned by the
// paperless list endpoint. The full lookup tables are included so the import
// UI can render filter dropdowns without fetching every document.
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

// PaperlessImportRequest asks the backend to pull a single Paperless document,
// parse it with the statement parser, and return the normalized transactions.
type PaperlessImportRequest struct {
	DocumentID int    `json:"documentId" binding:"required"`
	Extractor  string `json:"extractor"`
	Password   string `json:"password"`
	DateFormat string `json:"dateFormat"`
}

// RegisterRequest is the body for POST /api/v1/auth/register. SetupToken is an
// operator-owned secret (ADMIN_SETUP_TOKEN): when the email matches
// ADMIN_EMAILS the registration is refused unless it is present and correct,
// so the admin role can only be self-assigned with proof of privileged access
// and admin-listed addresses can't be squatted by an unverified registrant.
// The password must be at least 12 characters and at most 72 bytes (bcrypt's
// input limit; the maxbytes tag measures bytes, not characters).
type RegisterRequest struct {
	Email      string `json:"email" binding:"required,email"`
	Password   string `json:"password" binding:"required,min=12,maxbytes=72"`
	SetupToken string `json:"setupToken"`
}

// LoginRequest is the body for POST /api/v1/auth/login. It intentionally does
// not enforce a minimum length: accounts created under an older, weaker policy
// must still be able to sign in. The 72-byte cap mirrors bcrypt's limit.
type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,maxbytes=72"`
}

// AuthResponse is returned by the register and login endpoints. The access and
// refresh tokens are delivered as httpOnly cookies, never in the body.
type AuthResponse struct {
	User User `json:"user"`
}

// CreateAccountRequest is the body for POST /api/v1/accounts.
//
// Every text field carries the width of the column it lands in (name 255, bank
// 100, currency 3, color 7). Without the tag an over-long value reaches
// PostgreSQL and raises 22001, which the handlers can only report as a 500
// "internal server error" — a malformed request answered as a server fault.
type CreateAccountRequest struct {
	Name          string `json:"name" binding:"required,max=255"`
	AccountTypeID string `json:"accountTypeId" binding:"required"`
	Bank          string `json:"bank" binding:"max=100"`
	Currency      string `json:"currency" binding:"max=3"`
	Color         string `json:"color" binding:"max=7"`
	IsDefault     bool   `json:"isDefault"`
	// BillingDay is the day of the month on which billing cycles end (1-31).
	// Optional: when set, summary rows are generated for the account regardless
	// of its type; omitted stores NULL (no cycles, no summary rows).
	BillingDay *int `json:"billingDay" binding:"omitempty,min=1,max=31"`
}

// UpdateAccountRequest is a partial update for an account. Empty-string text
// fields are treated as "not provided" and preserve the stored value (so name
// and accountTypeId can never be blanked); BillingDay uses OptionalInt so an
// explicit null clears the stored value while an absent key leaves it
// untouched, and the *bool fields distinguish "not provided" from "set".
type UpdateAccountRequest struct {
	Name          string `json:"name" binding:"max=255"`
	AccountTypeID string `json:"accountTypeId"`
	Bank          string `json:"bank" binding:"omitempty,max=100"`
	Currency      string `json:"currency" binding:"omitempty,max=3"`
	Color         string `json:"color" binding:"omitempty,max=7"`
	IsDefault     *bool  `json:"isDefault"`
	// Closed marks the account closed (true) or reopens it (false). Absent
	// leaves the current value untouched.
	Closed *bool `json:"closed"`
	// BillingDay is the day of the month on which billing cycles end (1-31,
	// range-checked in the handler). An absent key leaves the current value
	// (and its derived billing cycles) untouched, an explicit value sets it,
	// and null clears it.
	BillingDay OptionalInt `json:"billingDay"`
}

// CreateCategoryRequest is the body for POST /api/v1/categories. Icon and Color
// carry their column widths (50 and 7) so an over-long value is a 400 field
// error instead of the 22001 the column would raise, which the handler can only
// report as a 500.
type CreateCategoryRequest struct {
	Name    string `json:"name" binding:"required,max=100"`
	Icon    string `json:"icon" binding:"max=50"`
	Color   string `json:"color" binding:"max=7"`
	GroupID string `json:"groupId" binding:"required"`
}

// UpdateCategoryRequest is the body for PUT /api/v1/categories/:id.
type UpdateCategoryRequest struct {
	Name    string `json:"name" binding:"max=100"`
	Icon    string `json:"icon" binding:"omitempty,max=50"`
	Color   string `json:"color" binding:"omitempty,max=7"`
	GroupID string `json:"groupId"`
}

// DeleteCategoryResult reports the side effects of deleting a category: how
// many transactions were uncategorized and how many rules were removed.
type DeleteCategoryResult struct {
	ClearedTransactions int `json:"clearedTransactions"`
	DeletedRules        int `json:"deletedRules"`
}

// CreateCategoryGroupRequest is the body for POST /api/v1/groups. Icon and Color
// carry their column widths (50 and 7); the id is bounded by the slug pattern in
// the handler.
type CreateCategoryGroupRequest struct {
	ID    string `json:"id" binding:"required"`
	Name  string `json:"name" binding:"required,max=100"`
	Icon  string `json:"icon" binding:"max=50"`
	Color string `json:"color" binding:"max=7"`
}

// UpdateCategoryGroupRequest is the body for PUT /api/v1/groups/:id.
type UpdateCategoryGroupRequest struct {
	Name  string `json:"name" binding:"max=100"`
	Icon  string `json:"icon" binding:"omitempty,max=50"`
	Color string `json:"color" binding:"omitempty,max=7"`
}

// CreateAccountTypeRequest is the admin-only body for POST /api/v1/account-types.
type CreateAccountTypeRequest struct {
	ID              string `json:"id" binding:"required"`
	Name            string `json:"name" binding:"required,max=100"`
	PositiveTxnType string `json:"positiveTxnType" binding:"required"`
}

// UpdateAccountTypeRequest is the admin-only body for PUT /api/v1/account-types/:id.
type UpdateAccountTypeRequest struct {
	Name            string `json:"name" binding:"max=100"`
	PositiveTxnType string `json:"positiveTxnType"`
}

// OptionalUUID distinguishes an absent JSON key from an explicit null so that a
// null value can clear a column while an absent key leaves it untouched.
type OptionalUUID struct {
	present bool
	value   *uuid.UUID
}

// UnmarshalJSON records the key as present in the body and parses the value:
// an explicit "null" clears the field (value becomes nil) while a UUID string
// is parsed normally.
func (o *OptionalUUID) UnmarshalJSON(data []byte) error {
	o.present = true
	if string(data) == "null" {
		o.value = nil
		return nil
	}
	var u uuid.UUID
	if err := json.Unmarshal(data, &u); err != nil {
		return err
	}
	o.value = &u
	return nil
}

// Set reports whether the key was present in the JSON body (including null).
func (o *OptionalUUID) Set() bool { return o != nil && o.present }

// Value returns the parsed UUID, or nil when the key was explicitly null.
func (o *OptionalUUID) Value() *uuid.UUID {
	if o == nil {
		return nil
	}
	return o.value
}

// OptionalInt distinguishes an absent JSON key from an explicit null so that a
// null value can clear an INT column while an absent key leaves it untouched.
type OptionalInt struct {
	present bool
	value   *int
}

// UnmarshalJSON records the key as present in the body and parses the value:
// an explicit "null" clears the field (value becomes nil) while a number is
// parsed normally.
func (o *OptionalInt) UnmarshalJSON(data []byte) error {
	o.present = true
	if string(data) == "null" {
		o.value = nil
		return nil
	}
	var v int
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	o.value = &v
	return nil
}

// Set reports whether the key was present in the JSON body (including null).
func (o *OptionalInt) Set() bool { return o != nil && o.present }

// Value returns the parsed int, or nil when the key was explicitly null.
func (o *OptionalInt) Value() *int {
	if o == nil {
		return nil
	}
	return o.value
}

// UpdateTransactionRequest is a partial update for a transaction. OptionalUUID
// fields allow an explicit null to clear a foreign key while an absent key
// leaves it untouched.
type UpdateTransactionRequest struct {
	CategoryID     OptionalUUID  `json:"categoryId"`
	Tags           *[]string     `json:"tags"`
	Notes          *string       `json:"notes"`
	PayeeID        OptionalUUID  `json:"payeeId"`
	Date           *string       `json:"date"`
	Description    *string       `json:"description"`
	Amount         *money.Amount `json:"amount"`
	Type           *string       `json:"type"`
	AccountID      *uuid.UUID    `json:"accountId"`
	BillingCycleID OptionalUUID  `json:"billingCycleId"`
}

// CreateTransactionRequest is the body for POST /api/v1/transactions.
//
// ClientKey is optional and makes the create idempotent: a request repeating a
// key the user has already used returns that transaction instead of inserting a
// second one, so a client may safely retry a create whose response was lost.
// It is client-generated and only has to be unique within the user's ledger.
type CreateTransactionRequest struct {
	AccountID      uuid.UUID    `json:"accountId" binding:"required"`
	Date           string       `json:"date" binding:"required"`
	Description    string       `json:"description" binding:"required"`
	Amount         money.Amount `json:"amount" binding:"required"`
	Type           string       `json:"type" binding:"required"`
	CategoryID     *uuid.UUID   `json:"categoryId"`
	PayeeID        *uuid.UUID   `json:"payeeId"`
	Tags           []string     `json:"tags"`
	Notes          string       `json:"notes"`
	BillingCycleID *uuid.UUID   `json:"billingCycleId"`
	ClientKey      string       `json:"clientKey"`
}

// BulkCategorizeRequest reassigns one category to many transactions at once.
// CategoryID accepts a category UUID or the "uncategorized" sentinel to clear
// the category on every selected transaction.
type BulkCategorizeRequest struct {
	TransactionIDs []uuid.UUID `json:"transactionIds" binding:"required"`
	CategoryID     string      `json:"categoryId" binding:"required"`
}

// BulkUpdatePayeeRequest reassigns one payee to many transactions at once.
type BulkUpdatePayeeRequest struct {
	TransactionIDs []uuid.UUID `json:"transactionIds" binding:"required"`
	PayeeID        uuid.UUID   `json:"payeeId" binding:"required"`
}

// BulkBillingCycleRequest attaches one billing cycle to many transactions.
type BulkBillingCycleRequest struct {
	TransactionIDs []uuid.UUID `json:"transactionIds" binding:"required"`
	BillingCycleID uuid.UUID   `json:"billingCycleId" binding:"required"`
}

// BulkDeleteTransactionsRequest lists the transactions to delete in one call.
type BulkDeleteTransactionsRequest struct {
	TransactionIDs []uuid.UUID `json:"transactionIds" binding:"required"`
}

// BulkUpdateTagsRequest adds and/or removes tags on many transactions at once.
// Add and Remove are applied in that order (remove wins when a tag appears in
// both), blank entries are ignored, and duplicates are collapsed.
type BulkUpdateTagsRequest struct {
	TransactionIDs []uuid.UUID `json:"transactionIds" binding:"required"`
	Add            []string    `json:"add"`
	Remove         []string    `json:"remove"`
}

// RenameTagRequest renames every use of one tag to another across the user's
// transactions. It is the "managed lookup" operation tags get in place of an
// id-based entity: the name is the identity.
type RenameTagRequest struct {
	From string `json:"from" binding:"required"`
	To   string `json:"to" binding:"required"`
}

// BulkLoanRequest attaches a batch of transactions to a single loan/EMI
// account (LoanAccountID set) or detaches them from whatever loan account they
// are currently attached to (LoanAccountID absent/null). One transaction can be
// attached to at most one loan account (UNIQUE on loan_attachments).
type BulkLoanRequest struct {
	TransactionIDs []uuid.UUID `json:"transactionIds" binding:"required"`
	LoanAccountID  *uuid.UUID  `json:"loanAccountId"`
}

// LoanSchedule is the optional amortization schedule of a Loan / EMI account:
// the terms the amortization table is generated from. Principal, ProcessingFee
// and the derived EMI are integer minor units; AnnualRateBps is integer basis
// points (950 = 9.50% p.a.), so no money arithmetic runs in float64. At most one
// schedule per loan account.
//
// ProcessingFee is what the lender charged, recorded for reference: it is never
// amortized, so the table repays the whole principal whatever the fee was (a
// lender that finances its charges instead has them entered in the principal).
// DisbursalDate is when the money was released; when it is set and the period
// from it to StartDate (the first installment date) is not a whole anchored
// month, the first installment carries day-count interest for the broken period
// and so splits differently from every later one.
type LoanSchedule struct {
	ID            uuid.UUID    `json:"id"`
	LoanAccountID uuid.UUID    `json:"loanAccountId"`
	Principal     money.Amount `json:"principal"`
	ProcessingFee money.Amount `json:"processingFee"`
	AnnualRateBps int          `json:"annualRateBps"`
	TenureMonths  int          `json:"tenureMonths"`
	StartDate     time.Time    `json:"startDate"`
	DisbursalDate *time.Time   `json:"disbursalDate,omitempty"`
	CreatedAt     time.Time    `json:"createdAt"`
	UpdatedAt     time.Time    `json:"updatedAt"`
}

// LoanScheduleRequest is the body for PUT /accounts/:id/loan-schedule. A zero
// rate is valid (an interest-free loan), so the rate is validated in the
// handler rather than by a binding rule. An absent ProcessingFee is a loan
// without a fee, and an empty DisbursalDate clears one.
type LoanScheduleRequest struct {
	Principal     money.Amount `json:"principal" binding:"required"`
	ProcessingFee money.Amount `json:"processingFee"`
	AnnualRateBps int          `json:"annualRateBps"`
	TenureMonths  int          `json:"tenureMonths" binding:"required"`
	StartDate     string       `json:"startDate" binding:"required"`
	DisbursalDate string       `json:"disbursalDate"`
}

// LoanScheduleEntry is one installment of the amortization table: the EMI split
// into principal and interest, with Balance the principal still outstanding
// after it. An entry is Paid when an EMI payment attached to the loan covers it
// — payments are matched in date order, the way a lender numbers installments —
// and then carries that transaction's id.
//
// The split is not uniform across a loan: the first entry differs when the
// disbursal-to-first-due period is a broken month (day-count interest), and
// every entry after a balance transfer is Recast (regenerated over the
// remaining tenure at the new balance, so its amount differs from the EMI of
// the entries before it). Cancelled marks an installment voided because a
// transfer settled the loan, which also makes it ineligible to be paid.
type LoanScheduleEntry struct {
	Number        int          `json:"number"`
	DueDate       time.Time    `json:"dueDate"`
	Amount        money.Amount `json:"amount"`
	Principal     money.Amount `json:"principal"`
	Interest      money.Amount `json:"interest"`
	Balance       money.Amount `json:"balance"`
	Paid          bool         `json:"paid"`
	TransactionID *uuid.UUID   `json:"transactionId,omitempty"`
	Recast        bool         `json:"recast"`
	Cancelled     bool         `json:"cancelled"`
}

// Loan transfer modes. A transfer always settles the source loan at its
// payoff — outstanding principal plus accrued interest; the mode decides what
// it does to the target.
const (
	// LoanTransferRecast adds the amount to the target's installments still due
	// after the transfer, so the target's debt grows by it.
	LoanTransferRecast = "recast"
	// LoanTransferOpens starts the target's schedule from the amount: the target
	// had no terms of its own.
	LoanTransferOpens = "opens"
	// LoanTransferTakeover leaves the target's own table alone and records the
	// amount as paid out of its disbursement — the target's proceeds settled the
	// source, so its debt is unchanged and its released cash is smaller.
	LoanTransferTakeover = "takeover"
)

// LoanPrincipalTransfer is one balance transfer between two loan accounts: the
// source loan's payoff on TransferDate — Principal plus AccruedInterest — is
// settled by the target loan in the way Mode describes. Both sides are derived
// from this row, so deleting it reverts them.
type LoanPrincipalTransfer struct {
	ID                  uuid.UUID `json:"id"`
	FromLoanAccountID   uuid.UUID `json:"fromLoanAccountId"`
	FromLoanAccountName string    `json:"fromLoanAccountName,omitempty"`
	ToLoanAccountID     uuid.UUID `json:"toLoanAccountId"`
	ToLoanAccountName   string    `json:"toLoanAccountName,omitempty"`
	// Amount is the payoff that moved: Principal plus the interest accrued
	// since the source's last EMI payment.
	Amount          money.Amount `json:"amount"`
	Principal       money.Amount `json:"principal"`
	AccruedInterest money.Amount `json:"accruedInterest"`
	TransferDate    time.Time    `json:"transferDate"`
	Mode            string       `json:"mode"`
	CreatedAt       time.Time    `json:"createdAt"`
}

// LoanPayoff is what settling a loan on a date costs: the principal it still
// owes plus the interest accrued since its last EMI payment. It is the quote a
// lender gives before a balance transfer, and exactly the amount a transfer
// moves.
type LoanPayoff struct {
	LoanAccountName string `json:"loanAccountName,omitempty"`
	// AsOf is the date quoted for; FromDate is the last EMI payment the accrual
	// runs from, and Days the days between them.
	AsOf                 time.Time    `json:"asOf"`
	FromDate             time.Time    `json:"fromDate"`
	Days                 int          `json:"days"`
	OutstandingPrincipal money.Amount `json:"outstandingPrincipal"`
	AccruedInterest      money.Amount `json:"accruedInterest"`
	Payoff               money.Amount `json:"payoff"`
}

// LoanTransferRequest is the body for POST /accounts/:id/loan-transfer, where
// :id is the source loan. The amount is never supplied by the caller: it is the
// source loan's outstanding principal, which is what a balance transfer settles.
//
// Mode picks what the target does with it (recast, opens, takeover); an empty
// Mode means recast when the target already has a schedule and opens when it has
// none, which is what a caller that does not care gets. The target terms are
// required only for the opens case, and are ignored otherwise.
type LoanTransferRequest struct {
	ToLoanAccountID     uuid.UUID `json:"toLoanAccountId" binding:"required"`
	TransferDate        string    `json:"transferDate" binding:"required"`
	Mode                string    `json:"mode"`
	TargetAnnualRateBps *int      `json:"targetAnnualRateBps"`
	TargetTenureMonths  *int      `json:"targetTenureMonths"`
	TargetStartDate     string    `json:"targetStartDate"`
}

// LoanDisbursement is what a loan actually released, and the bank credit it is
// reconciled against. Net is the sanctioned principal less the lender's fee and
// less any takeover this loan funded — the cash the borrower should have
// received — and Verified means the linked credit matches it exactly.
type LoanDisbursement struct {
	Sanctioned    money.Amount `json:"sanctioned"`
	ProcessingFee money.Amount `json:"processingFee"`
	PaidOut       money.Amount `json:"paidOut"`
	Net           money.Amount `json:"net"`
	// CreditTransactionID and CreditAmount are the linked bank credit, absent
	// until one is linked.
	CreditTransactionID *uuid.UUID    `json:"creditTransactionId,omitempty"`
	CreditAmount        *money.Amount `json:"creditAmount,omitempty"`
	Verified            bool          `json:"verified"`
	// Difference is CreditAmount - Net, so a positive value means more money
	// arrived than the schedule implies.
	Difference money.Amount `json:"difference"`
}

// LoanDisbursementRequest is the body for PUT /accounts/:id/loan-disbursement:
// the bank credit transaction that released the loan.
type LoanDisbursementRequest struct {
	TransactionID uuid.UUID `json:"transactionId" binding:"required"`
}

// DeleteLoanDisbursementResult reports how many links a delete removed (0 or 1).
type DeleteLoanDisbursementResult struct {
	Deleted int64 `json:"deleted"`
}

// LoanTransferResult is the response of POST /accounts/:id/loan-transfer: the
// recorded transfer plus both loans' regenerated details, so the caller can
// render both sides without a second request.
type LoanTransferResult struct {
	Transfer LoanPrincipalTransfer `json:"transfer"`
	Source   LoanScheduleDetail    `json:"source"`
	Target   LoanScheduleDetail    `json:"target"`
}

// DeleteLoanTransferResult reports how many transfers a delete removed (0 or 1).
type DeleteLoanTransferResult struct {
	Deleted int64 `json:"deleted"`
}

// LoanScheduleDetail is the response of GET /accounts/:id/loan-schedule. The
// schedule is optional, so an unconfigured loan returns schedule: null with an
// empty table and zeroed totals instead of a 404; the account itself missing or
// not being a loan account is still an error.
type LoanScheduleDetail struct {
	Schedule        *LoanSchedule `json:"schedule"`
	LoanAccountName string        `json:"loanAccountName,omitempty"`
	// EMI is the installment in force: the amount of the current table
	// segment, which after a balance transfer is the recast one. Individual
	// entries still carry their own Amount, since a stub first period and every
	// recast segment differ from it.
	EMI           money.Amount        `json:"emi"`
	TotalInterest money.Amount        `json:"totalInterest"`
	TotalPayable  money.Amount        `json:"totalPayable"`
	Entries       []LoanScheduleEntry `json:"entries"`
	// Transfers touching this loan, in date order, whether it gave or received
	// the balance. SettledOn is set when one of them settled this loan (it gave
	// the balance away), which cancels every later installment and zeroes the
	// outstanding principal.
	Transfers []LoanPrincipalTransfer `json:"transfers"`
	SettledOn *time.Time              `json:"settledOn,omitempty"`
	// Disbursement is what the loan released, and the bank credit it is
	// reconciled against. Absent when the loan has no schedule.
	Disbursement *LoanDisbursement `json:"disbursement,omitempty"`
	// LastPaidDate is the date of the payment covering the highest covered
	// installment: what a settlement's interest accrues from. Absent until an
	// installment is paid.
	LastPaidDate *time.Time `json:"lastPaidDate,omitempty"`
	// Progress derived from the EMI transactions attached to the loan. Paid
	// installments are counted in date order; Credits reduces PaidAmount.
	PaidInstallments     int          `json:"paidInstallments"`
	PaidAmount           money.Amount `json:"paidAmount"`
	PrincipalPaid        money.Amount `json:"principalPaid"`
	InterestPaid         money.Amount `json:"interestPaid"`
	OutstandingPrincipal money.Amount `json:"outstandingPrincipal"`
	// NextDueDate is the next unpaid installment's due date; absent once every
	// installment is covered.
	NextDueDate *time.Time `json:"nextDueDate,omitempty"`
	Completed   bool       `json:"completed"`
}

// DeleteLoanScheduleResult reports how many schedules a delete removed (0 or 1).
type DeleteLoanScheduleResult struct {
	Deleted int64 `json:"deleted"`
}

// ImportRequest is the body for POST /api/v1/transactions/import. DuplicateAction
// is "skip" (drop rows that already exist) or "keep" (insert everything);
// BillingCycleID attaches imports for an account with a configured billing day
// to a specific cycle; and
// PaperlessDocumentIDs are tagged after a successful import.
type ImportRequest struct {
	AccountID            uuid.UUID           `json:"accountId"`
	Transactions         []ImportTransaction `json:"transactions"`
	DuplicateAction      string              `json:"duplicateAction"`      // "skip" | "keep"
	BillingCycleID       *uuid.UUID          `json:"billingCycleId"`       // imports for an account with a billing day: attach every imported transaction to this cycle
	PaperlessDocumentIDs []int               `json:"paperlessDocumentIds"` // tag these Paperless docs after a successful import
}

// ValidateTransactionsRequest asks whether a set of candidate transactions
// already exist in a given account. It is a read-only check (no rows are
// written) that mirrors the import endpoint's duplicate detection.
type ValidateTransactionsRequest struct {
	AccountID    uuid.UUID           `json:"accountId"`
	Transactions []ImportTransaction `json:"transactions"`
}

// ValidateTransactionResult reports whether a single candidate transaction
// already exists in the target account. Index aligns with the request's
// Transactions slice so the client can map results back to its preview rows.
type ValidateTransactionResult struct {
	Index       int          `json:"index"`
	Exists      bool         `json:"exists"`
	Date        string       `json:"date"`
	Description string       `json:"description"`
	Amount      money.Amount `json:"amount"`
	Type        string       `json:"type"`
}

// ValidateTransactionsResponse summarizes a validation run for the frontend's
// import preview.
type ValidateTransactionsResponse struct {
	Total         int                         `json:"total"`
	ExistingCount int                         `json:"existingCount"`
	MissingCount  int                         `json:"missingCount"`
	Results       []ValidateTransactionResult `json:"results"`
}

// ImportTransaction is a single candidate row in an import or validation batch.
type ImportTransaction struct {
	Date        string       `json:"date"`
	Description string       `json:"description"`
	Amount      money.Amount `json:"amount"`
	Type        string       `json:"type"`
	PayeeID     *uuid.UUID   `json:"payeeId"`
}

// CreateRuleRequest is the body for POST /api/v1/rules. Pattern carries its
// column width (500): without it an over-long pattern reaches `rules.pattern
// VARCHAR(500)` and raises 22001, which the handler can only answer with a 500.
type CreateRuleRequest struct {
	Pattern    string     `json:"pattern" binding:"required,max=500"`
	MatchType  string     `json:"matchType"`
	CategoryID uuid.UUID  `json:"categoryId" binding:"required"`
	PayeeID    *uuid.UUID `json:"payeeId"`
	Priority   int        `json:"priority"`

	AccountID        *uuid.UUID    `json:"accountId"`
	FilterCategoryID *uuid.UUID    `json:"filterCategoryId"`
	FilterPayeeID    *uuid.UUID    `json:"filterPayeeId"`
	MinAmount        *money.Amount `json:"minAmount"`
	MaxAmount        *money.Amount `json:"maxAmount"`
	TxnType          string        `json:"txnType"`
	DateFrom         string        `json:"dateFrom"`
	DateTo           string        `json:"dateTo"`
	IsLinked         *bool         `json:"isLinked"`
	IsRecurring      *bool         `json:"isRecurring"`
	AddTags          []string      `json:"addTags"`
	Notes            string        `json:"notes"`
}

// UpdateRuleRequest is the body for PUT /api/v1/rules/:id. Pattern is not
// `required` because the handler rejects an empty one with a message of its
// own; the max is the column width, so an over-long pattern is a 400 rather
// than the 22001 the column raises.
type UpdateRuleRequest struct {
	Pattern    string     `json:"pattern" binding:"max=500"`
	MatchType  string     `json:"matchType"`
	CategoryID uuid.UUID  `json:"categoryId"`
	PayeeID    *uuid.UUID `json:"payeeId"`
	Priority   int        `json:"priority"`

	AccountID        *uuid.UUID    `json:"accountId"`
	FilterCategoryID *uuid.UUID    `json:"filterCategoryId"`
	FilterPayeeID    *uuid.UUID    `json:"filterPayeeId"`
	MinAmount        *money.Amount `json:"minAmount"`
	MaxAmount        *money.Amount `json:"maxAmount"`
	TxnType          string        `json:"txnType"`
	DateFrom         string        `json:"dateFrom"`
	DateTo           string        `json:"dateTo"`
	IsLinked         *bool         `json:"isLinked"`
	IsRecurring      *bool         `json:"isRecurring"`
	AddTags          []string      `json:"addTags"`
	Notes            string        `json:"notes"`
}

// CreatePayeeRequest is the body for POST /api/v1/payees.
type CreatePayeeRequest struct {
	Name      string     `json:"name" binding:"required,max=255"`
	AccountID *uuid.UUID `json:"accountId"`
}

// CreateLinkRequest is the body for POST /api/v1/links.
type CreateLinkRequest struct {
	Type      string    `json:"type" binding:"required"`
	FromTxnID uuid.UUID `json:"fromTxnId" binding:"required"`
	ToTxnID   uuid.UUID `json:"toTxnId" binding:"required"`
	Notes     string    `json:"notes"`
}

// BulkCreateLinksRequest creates many links in one call.
type BulkCreateLinksRequest struct {
	Links []CreateLinkRequest `json:"links" binding:"required"`
}

// BulkDeleteLinksRequest lists the link IDs to delete in one call.
type BulkDeleteLinksRequest struct {
	IDs []uuid.UUID `json:"ids" binding:"required"`
}

// DashboardSummary aggregates a user's financial overview for the dashboard:
// account/transaction counts, income and expense totals, spending and income
// breakdowns by category, a monthly trend, and the most recent transactions.
// In billing-cycle view (groupBy=billing_cycle) the totals reflect the current
// statement period, BillingCycleTrend replaces MonthlyTrend, and
// CurrentCycle describes that in-progress period.
type DashboardSummary struct {
	TotalAccounts      int             `json:"totalAccounts"`
	TotalTransactions  int             `json:"totalTransactions"`
	TotalIncome        money.Amount    `json:"totalIncome"`
	TotalExpense       money.Amount    `json:"totalExpense"`
	ByCategory         []CategorySpend `json:"byCategory"`
	IncomeByCategory   []CategorySpend `json:"incomeByCategory"`
	MonthlyTrend       []MonthlyData   `json:"monthlyTrend"`
	RecentTransactions []Transaction   `json:"recentTransactions"`
	// Billing-cycle view (groupBy=billing_cycle): populated when the dashboard
	// is framed around statement periods for a single billing-day account.
	CurrentCycle      *CurrentCycleInfo       `json:"currentCycle,omitempty"`
	BillingCycleTrend []BillingCycleTrendItem `json:"billingCycleTrend,omitempty"`
}

// CurrentCycleInfo describes the billing cycle currently in progress for an
// account in billing-cycle dashboard view.
type CurrentCycleInfo struct {
	ID        uuid.UUID `json:"id"`
	StartDate time.Time `json:"startDate"`
	EndDate   time.Time `json:"endDate"`
	Label     string    `json:"label"`
}

// BillingCycleTrendItem holds income and expense totals for one billing cycle,
// keyed by its label (e.g. "Aug 2026").
type BillingCycleTrendItem struct {
	Label     string       `json:"label"`
	StartDate time.Time    `json:"startDate"`
	EndDate   time.Time    `json:"endDate"`
	Income    money.Amount `json:"income"`
	Expense   money.Amount `json:"expense"`
}

// CategorySpend aggregates spend/income for a single category.
type CategorySpend struct {
	CategoryID    uuid.UUID    `json:"categoryId"`
	CategoryName  string       `json:"categoryName"`
	CategoryColor string       `json:"categoryColor"`
	CategoryIcon  string       `json:"categoryIcon"`
	Total         money.Amount `json:"total"`
	Count         int          `json:"count"`
}

// MonthlyData holds income and expense totals for one month (keyed "YYYY-MM").
type MonthlyData struct {
	Month   string       `json:"month"`
	Income  money.Amount `json:"income"`
	Expense money.Amount `json:"expense"`
}

// Money-flow Sankey types. GetMoneyFlow aggregates every transaction in a
// window into a left-to-right graph: money sources (income categories) flow
// into accounts, out into spending categories, and on to payees. The graph is
// a DAG (a Sankey cannot render cycles): cross-account transfers, refunds,
// cashbacks and bill payments are drawn as account-to-account edges after
// reciprocal pairs are netted and remaining cycles broken, and are also
// summarized per type in LinkSummary.

// MoneyFlowNode is one node of the money-flow graph. Kind is "income",
// "account", "category", or "payee"; ID is stable and stage-prefixed (e.g.
// "account:<uuid>", "category:uncategorized", "income:other"). Color is the
// display color (the category's base-group color for category nodes, the
// account color for account nodes) and Group carries the category group id for
// category and income nodes.
type MoneyFlowNode struct {
	ID    string       `json:"id"`
	Name  string       `json:"name"`
	Kind  string       `json:"kind"`
	Color string       `json:"color,omitempty"`
	Group string       `json:"group,omitempty"`
	Total money.Amount `json:"total"`
}

// MoneyFlowEdge is one aggregated flow between two MoneyFlowNode IDs.
type MoneyFlowEdge struct {
	Source string       `json:"source"`
	Target string       `json:"target"`
	Value  money.Amount `json:"value"`
}

// MoneyFlowLinkSummary is a per-type rollup of the user's transaction links in
// the same window (transfers, refunds, cashbacks, bill payments), shown beside
// the graph rather than drawn as account-to-account edges.
type MoneyFlowLinkSummary struct {
	Type  string       `json:"type"`
	Count int          `json:"count"`
	Total money.Amount `json:"total"`
}

// MoneyFlowGraph is the response of GET /api/v1/dashboard/money-flow.
type MoneyFlowGraph struct {
	Nodes        []MoneyFlowNode        `json:"nodes"`
	Links        []MoneyFlowEdge        `json:"links"`
	TotalIncome  money.Amount           `json:"totalIncome"`
	TotalExpense money.Amount           `json:"totalExpense"`
	LinkSummary  []MoneyFlowLinkSummary `json:"linkSummary"`
}

// Circular-money report types. The Sankey has to be acyclic, so the account
// graph silently nets reciprocal pairs and drops the DFS back edges that close
// a cycle; GetLinkCycles reports exactly those discarded cycles plus the
// account-to-account flows that have no counterpart in the opposite direction.

// LinkCycleAccount is one participant of a circular flow.
type LinkCycleAccount struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

// LinkCycleLeg is one directed account-to-account flow inside a cycle, with the
// link types that make it up.
type LinkCycleLeg struct {
	FromAccountID    string              `json:"fromAccountId"`
	FromAccountName  string              `json:"fromAccountName"`
	FromAccountColor string              `json:"fromAccountColor,omitempty"`
	ToAccountID      string              `json:"toAccountId"`
	ToAccountName    string              `json:"toAccountName"`
	ToAccountColor   string              `json:"toAccountColor,omitempty"`
	Amount           money.Amount        `json:"amount"`
	Count            int                 `json:"count"`
	Types            []LinkFlowTypeTotal `json:"types"`
}

// LinkCycle is one circular money flow between accounts. Kind is "reciprocal"
// for a pair that flows both ways (netted into one edge for the Sankey) or
// "cycle" for a longer loop broken by dropping its back edge. Net is the
// smallest leg — the amount that actually circulates the whole loop — and Gross
// the sum of the legs.
type LinkCycle struct {
	Kind         string             `json:"kind"`
	Accounts     []LinkCycleAccount `json:"accounts"`
	Legs         []LinkCycleLeg     `json:"legs"`
	Net          money.Amount       `json:"net"`
	Gross        money.Amount       `json:"gross"`
	Transactions int                `json:"transactions"`
}

// LinkFlowTypeTotal is a per-link-type rollup of an account-to-account flow.
type LinkFlowTypeTotal struct {
	Type  string       `json:"type"`
	Count int          `json:"count"`
	Total money.Amount `json:"total"`
}

// LinkOneSidedFlow is a directed account-to-account flow whose counterpart is
// missing: no link moves money between the same two accounts in the opposite
// direction within the window. A legitimate one-way flow (a card bill paid from
// a bank account, a refund landing on a different card) looks the same as a
// half-entered transfer, which is why it is surfaced for review rather than
// corrected.
type LinkOneSidedFlow struct {
	FromAccountID    string              `json:"fromAccountId"`
	FromAccountName  string              `json:"fromAccountName"`
	FromAccountColor string              `json:"fromAccountColor,omitempty"`
	ToAccountID      string              `json:"toAccountId"`
	ToAccountName    string              `json:"toAccountName"`
	ToAccountColor   string              `json:"toAccountColor,omitempty"`
	Total            money.Amount        `json:"total"`
	Count            int                 `json:"count"`
	Types            []LinkFlowTypeTotal `json:"types"`
}

// LinkCycleReport is the response of GET /api/v1/links/cycles.
type LinkCycleReport struct {
	Cycles []LinkCycle `json:"cycles"`
	// TotalCircular is the sum of every cycle's Net: the money that travels a
	// full loop between the user's own accounts.
	TotalCircular money.Amount `json:"totalCircular"`
	// OneSidedFlows are directed pairs with no flow in the opposite direction.
	OneSidedFlows []LinkOneSidedFlow `json:"oneSidedFlows"`
}

// Cash-flow calendar types. GetCashFlowCalendar returns one CashFlowCalendarDay
// per day that has transactions in the window, plus optional overlays when a
// single account is selected: its billing-cycle boundaries (CashFlowCalendarCycle)
// and the synthetic summary rows already shown in the transactions list
// (CashFlowCalendarMarker). Days with no transactions are omitted; the client
// fills the gaps to render a continuous GitHub-style heatmap.

// CashFlowCalendarDay is one day of daily net flow. Net is income minus
// expense; Count is the number of transactions posted that day.
type CashFlowCalendarDay struct {
	Date    string       `json:"date"`
	Income  money.Amount `json:"income"`
	Expense money.Amount `json:"expense"`
	Net     money.Amount `json:"net"`
	Count   int          `json:"count"`
}

// CashFlowCalendarMarker is a synthetic summary point overlaid on the calendar:
// a month-end "Running balance" for accounts without a billing day (kind
// "balance") or a per-cycle "Total outstanding" for accounts with one (kind
// "outstanding"). Amount matches the corresponding row in the transactions list.
type CashFlowCalendarMarker struct {
	Date   string       `json:"date"`
	Label  string       `json:"label"`
	Kind   string       `json:"kind"`
	Amount money.Amount `json:"amount"`
}

// CashFlowCalendarCycle is a billing-cycle boundary so the heatmap can mark
// statement periods. Outstanding is the running balance through the cycle end.
type CashFlowCalendarCycle struct {
	ID          uuid.UUID    `json:"id"`
	Label       string       `json:"label"`
	StartDate   time.Time    `json:"startDate"`
	EndDate     time.Time    `json:"endDate"`
	Outstanding money.Amount `json:"outstanding"`
}

// CashFlowCalendar is the response of GET /api/v1/dashboard/cash-flow-calendar.
type CashFlowCalendar struct {
	Days         []CashFlowCalendarDay    `json:"days"`
	Markers      []CashFlowCalendarMarker `json:"markers"`
	Cycles       []CashFlowCalendarCycle  `json:"cycles"`
	TotalIncome  money.Amount             `json:"totalIncome"`
	TotalExpense money.Amount             `json:"totalExpense"`
	Net          money.Amount             `json:"net"`
	// MaxAbsNet is the largest |net| among the returned days, used by the client
	// to scale the heatmap intensity without a second pass.
	MaxAbsNet money.Amount `json:"maxAbsNet"`
}

// Money-flow timeline types. GetMoneyFlowTimeline returns one entry per calendar
// month (or billing cycle) so the Money Flow page can show a timeline strip and
// scrub the Sankey's window to a period.

// MoneyFlowTimelinePeriod is one period of the flow timeline. StartDate and
// EndDate are inclusive YYYY-MM-DD bounds suitable for re-querying the flow.
type MoneyFlowTimelinePeriod struct {
	Key       string       `json:"key"`
	Label     string       `json:"label"`
	StartDate string       `json:"startDate"`
	EndDate   string       `json:"endDate"`
	Income    money.Amount `json:"income"`
	Expense   money.Amount `json:"expense"`
	Net       money.Amount `json:"net"`
}

// MoneyFlowTimeline is the response of GET /api/v1/dashboard/money-flow/timeline.
type MoneyFlowTimeline struct {
	GroupBy string                    `json:"groupBy"`
	Periods []MoneyFlowTimelinePeriod `json:"periods"`
}

// TransferSuggestion proposes that two transactions be linked, e.g. a debit and
// a matching credit in different accounts. Score (0-100) estimates how
// confident the suggestion is.
type TransferSuggestion struct {
	DebitTxn  Transaction `json:"debitTxn"`
	CreditTxn Transaction `json:"creditTxn"`
	Score     float64     `json:"score"`
}

// RecurringSeries is a user-defined expectation of a repeating charge or income
// (rent, salary, a subscription). It is a template only: FinTrak never
// auto-creates transactions from it and never auto-links transactions to it.
// The forecast projects its schedule and GetRecurringSuggestions proposes
// matching transactions; the user confirms each link explicitly through
// recurring_attachments.
type RecurringSeries struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Type        string    `json:"type"`
	// Frequency is one of "daily", "weekly", "monthly", "yearly"; Interval is
	// how many frequency units apart consecutive occurrences are (>= 1).
	Frequency  string     `json:"frequency"`
	Interval   int        `json:"interval"`
	CategoryID *uuid.UUID `json:"categoryId,omitempty"`
	PayeeID    *uuid.UUID `json:"payeeId,omitempty"`
	Active     bool       `json:"active"`
	Notes      string     `json:"notes"`
	CreatedAt  time.Time  `json:"createdAt"`
	// Derived from the series' terms (recurring_series_terms), not stored on
	// the series row. AccountID/Amount are the term in effect today; StartDate
	// is the earliest term start and EndDate the latest term end (nil while
	// open-ended).
	AccountID uuid.UUID    `json:"accountId"`
	Amount    money.Amount `json:"amount"`
	StartDate time.Time    `json:"startDate"`
	EndDate   *time.Time   `json:"endDate,omitempty"`
	// Joined fields
	AccountName   string `json:"accountName,omitempty"`
	CategoryName  string `json:"categoryName,omitempty"`
	CategoryIcon  string `json:"categoryIcon,omitempty"`
	CategoryColor string `json:"categoryColor,omitempty"`
	Payee         string `json:"payee,omitempty"`
	// Derived fields (computed in the handler, not stored)
	// NextDueDate is the next occurrence on/after today (nil once the series
	// has ended); MonthlyAmount normalizes the series to an estimated monthly
	// cost in minor units; AttachedCount is how many transactions are linked.
	NextDueDate   *time.Time   `json:"nextDueDate,omitempty"`
	MonthlyAmount money.Amount `json:"monthlyAmount"`
	AttachedCount int          `json:"attachedCount"`
}

// RecurringForecastItem is a single projected occurrence of a recurring series.
// Matched reports whether an attached transaction already falls on (or near)
// that occurrence.
type RecurringForecastItem struct {
	Date    time.Time    `json:"date"`
	Amount  money.Amount `json:"amount"`
	Type    string       `json:"type"`
	Matched bool         `json:"matched"`
}

// RecurringSuggestion proposes that a transaction satisfies one occurrence of a
// recurring series. Score (0-100) estimates confidence; OccurrenceDate is the
// nearest expected occurrence and DaysOff its distance in days.
type RecurringSuggestion struct {
	Txn            Transaction `json:"txn"`
	Score          float64     `json:"score"`
	OccurrenceDate time.Time   `json:"occurrenceDate"`
	DaysOff        float64     `json:"daysOff"`
}

// RecurringSeriesRange is one date-ranged amount/account entry supplied when
// creating or editing a series. The end date is exclusive: an entry "from x to
// y" applies from x up to (but not including) y.
type RecurringSeriesRange struct {
	StartDate string       `json:"startDate" binding:"required"`
	EndDate   string       `json:"endDate"`
	Amount    money.Amount `json:"amount" binding:"required"`
	AccountID uuid.UUID    `json:"accountId" binding:"required"`
}

// CreateRecurringSeriesRequest is the body for POST /api/v1/recurring. Either
// supply the full Ranges list (which also derives the subscription's start and
// end), or a single StartDate + AccountID + Amount.
type CreateRecurringSeriesRequest struct {
	AccountID   *uuid.UUID             `json:"accountId"`
	Name        string                 `json:"name" binding:"required,max=255"`
	Description string                 `json:"description"`
	Amount      *money.Amount          `json:"amount"`
	Type        string                 `json:"type" binding:"required"`
	Frequency   string                 `json:"frequency" binding:"required"`
	Interval    int                    `json:"interval"`
	StartDate   string                 `json:"startDate"`
	EndDate     string                 `json:"endDate"`
	CategoryID  *uuid.UUID             `json:"categoryId"`
	PayeeID     *uuid.UUID             `json:"payeeId"`
	Active      *bool                  `json:"active"`
	Notes       string                 `json:"notes"`
	Ranges      []RecurringSeriesRange `json:"ranges"`
}

// UpdateRecurringSeriesRequest is a partial update for a recurring series.
// Pointer fields distinguish "not provided" from a zero value; CategoryID and
// PayeeID use OptionalUUID so an explicit null clears them. The series' amount,
// account and period live on its terms: when Ranges is non-empty the whole
// range list is replaced; otherwise an Amount/AccountID change is recorded as a
// new range starting at EffectiveDate (default: today).
type UpdateRecurringSeriesRequest struct {
	AccountID     *uuid.UUID             `json:"accountId"`
	Name          *string                `json:"name" binding:"omitempty,max=255"`
	Description   *string                `json:"description"`
	Amount        *money.Amount          `json:"amount"`
	Type          *string                `json:"type"`
	Frequency     *string                `json:"frequency"`
	Interval      *int                   `json:"interval"`
	CategoryID    OptionalUUID           `json:"categoryId"`
	PayeeID       OptionalUUID           `json:"payeeId"`
	Active        *bool                  `json:"active"`
	Notes         *string                `json:"notes"`
	EffectiveDate *string                `json:"effectiveDate"`
	Ranges        []RecurringSeriesRange `json:"ranges"`
}

// RecurringSeriesTerm is one date-ranged amount/account segment of a recurring
// series. A transaction matches a term only when its date falls within
// [StartDate, EndDate) (EndDate nil = open-ended), and an occurrence uses the
// amount/account of the term covering it.
type RecurringSeriesTerm struct {
	ID          uuid.UUID    `json:"id"`
	SeriesID    uuid.UUID    `json:"seriesId"`
	StartDate   time.Time    `json:"startDate"`
	EndDate     *time.Time   `json:"endDate,omitempty"`
	Amount      money.Amount `json:"amount"`
	AccountID   uuid.UUID    `json:"accountId"`
	AccountName string       `json:"accountName,omitempty"`
	CreatedAt   time.Time    `json:"createdAt"`
}

// CreateRecurringSeriesTermRequest is the body for PUT
// /api/v1/recurring/:id/terms. It records a date-ranged amount/account entry.
type CreateRecurringSeriesTermRequest struct {
	StartDate string       `json:"startDate" binding:"required"`
	EndDate   string       `json:"endDate"`
	Amount    money.Amount `json:"amount" binding:"required"`
	AccountID uuid.UUID    `json:"accountId" binding:"required"`
}

// UpdateRecurringSeriesTermRequest is the body for PUT
// /api/v1/recurring/:id/terms/:termId. Pointer fields distinguish "not
// provided" from a zero value; EndDate "" clears the range end.
type UpdateRecurringSeriesTermRequest struct {
	StartDate *string       `json:"startDate"`
	EndDate   *string       `json:"endDate"`
	Amount    *money.Amount `json:"amount"`
	AccountID *uuid.UUID    `json:"accountId"`
}

// RecurringAttachRequest links a batch of transactions to one recurring series.
// A transaction may be attached to at most one series (UNIQUE on
// recurring_attachments.transaction_id).
type RecurringAttachRequest struct {
	SeriesID       uuid.UUID   `json:"seriesId" binding:"required"`
	TransactionIDs []uuid.UUID `json:"transactionIds" binding:"required"`
}

// RecurringDetachRequest unlinks a batch of transactions from whatever series
// they are attached to.
type RecurringDetachRequest struct {
	TransactionIDs []uuid.UUID `json:"transactionIds" binding:"required"`
}

// User-level backup bundle. Unlike the account CSV export, a user's data is a
// graph (links join two transactions, billing cycles and recurring/loan
// attachments reference other rows, rules/payees/categories are shared), so a
// backup is a single versioned JSON document. The original IDs are exported as
// opaque in-bundle references; an import mints fresh IDs and rewrites every
// reference through an ID map, which makes a bundle portable across users and
// across FinTrak instances.

const (
	// BackupFormat identifies a FinTrak user backup document.
	BackupFormat = "fintrak.backup"
	// BackupVersion is the current bundle schema version.
	BackupVersion = 1
)

// BackupBundle is a complete snapshot of everything a single user owns.
type BackupBundle struct {
	Format     string    `json:"format"`
	Version    int       `json:"version"`
	ExportedAt time.Time `json:"exportedAt"`
	// Settings carries non-secret preferences only. The Paperless API token and
	// password hash are never exported.
	Settings             *BackupSettings             `json:"settings,omitempty"`
	Accounts             []BackupAccount             `json:"accounts"`
	CategoryGroups       []BackupCategoryGroup       `json:"categoryGroups"`
	Categories           []BackupCategory            `json:"categories"`
	Payees               []BackupPayee               `json:"payees"`
	BillingCycles        []BackupBillingCycle        `json:"billingCycles"`
	Transactions         []BackupTransaction         `json:"transactions"`
	Links                []BackupLink                `json:"links"`
	LoanAttachments      []BackupLoanAttachment      `json:"loanAttachments"`
	LoanSchedules        []BackupLoanSchedule        `json:"loanSchedules"`
	LoanTransfers        []BackupLoanTransfer        `json:"loanTransfers"`
	LoanDisbursements    []BackupLoanDisbursement    `json:"loanDisbursements"`
	RecurringSeries      []BackupRecurringSeries     `json:"recurringSeries"`
	RecurringTerms       []BackupRecurringTerm       `json:"recurringTerms"`
	RecurringAttachments []BackupRecurringAttachment `json:"recurringAttachments"`
	Rules                []BackupRule                `json:"rules"`
}

// BackupSettings is the non-secret subset of a user's settings.
type BackupSettings struct {
	PaperlessURL string `json:"paperlessUrl,omitempty"`
	PaperlessTag string `json:"paperlessTag,omitempty"`
	PageSize     *int   `json:"pageSize,omitempty"`
}

// BackupAccount is one account in a backup bundle.
type BackupAccount struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	AccountTypeID string    `json:"accountTypeId"`
	Bank          string    `json:"bank"`
	Currency      string    `json:"currency"`
	Color         string    `json:"color"`
	IsDefault     bool      `json:"isDefault"`
	BillingDay    *int      `json:"billingDay,omitempty"`
	Closed        bool      `json:"closed"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// BackupCategoryGroup is a user-owned custom category group.
type BackupCategoryGroup struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Icon      string `json:"icon"`
	Color     string `json:"color"`
	SortOrder int    `json:"sortOrder"`
}

// BackupCategory is a category referenced by the user's data. Global marks an
// admin-created category (user_id NULL) that the user references but does not
// own; on import it is matched to the target instance's global category (or
// recreated as a user-owned copy when absent).
type BackupCategory struct {
	ID      uuid.UUID `json:"id"`
	Name    string    `json:"name"`
	Icon    string    `json:"icon"`
	Color   string    `json:"color"`
	GroupID string    `json:"groupId"`
	Global  bool      `json:"global,omitempty"`
}

// BackupPayee is one payee in a backup bundle.
type BackupPayee struct {
	ID        uuid.UUID  `json:"id"`
	Name      string     `json:"name"`
	AccountID *uuid.UUID `json:"accountId,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

// BackupBillingCycle is one persisted billing period.
type BackupBillingCycle struct {
	ID        uuid.UUID `json:"id"`
	AccountID uuid.UUID `json:"accountId"`
	StartDate string    `json:"startDate"`
	EndDate   string    `json:"endDate"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"createdAt"`
}

// BackupTransaction is one transaction. Dates are ISO "YYYY-MM-DD" strings.
type BackupTransaction struct {
	ID             uuid.UUID    `json:"id"`
	AccountID      uuid.UUID    `json:"accountId"`
	Date           string       `json:"date"`
	Description    string       `json:"description"`
	Amount         money.Amount `json:"amount"`
	Type           string       `json:"type"`
	CategoryID     *uuid.UUID   `json:"categoryId,omitempty"`
	Tags           []string     `json:"tags"`
	Notes          string       `json:"notes"`
	PayeeID        *uuid.UUID   `json:"payeeId,omitempty"`
	BillingCycleID *uuid.UUID   `json:"billingCycleId,omitempty"`
	CreatedAt      time.Time    `json:"createdAt"`
	UpdatedAt      time.Time    `json:"updatedAt"`
}

// BackupLink is a link between two transactions.
type BackupLink struct {
	ID        uuid.UUID `json:"id"`
	Type      string    `json:"type"`
	FromTxnID uuid.UUID `json:"fromTxnId"`
	ToTxnID   uuid.UUID `json:"toTxnId"`
	Notes     string    `json:"notes"`
	CreatedAt time.Time `json:"createdAt"`
}

// BackupLoanAttachment attaches a transaction to a loan account.
type BackupLoanAttachment struct {
	ID            uuid.UUID `json:"id"`
	LoanAccountID uuid.UUID `json:"loanAccountId"`
	TransactionID uuid.UUID `json:"transactionId"`
	CreatedAt     time.Time `json:"createdAt"`
}

// BackupLoanSchedule is a loan account's optional amortization schedule (the
// terms the amortization table is generated from).
type BackupLoanSchedule struct {
	ID            uuid.UUID    `json:"id"`
	LoanAccountID uuid.UUID    `json:"loanAccountId"`
	Principal     money.Amount `json:"principal"`
	ProcessingFee money.Amount `json:"processingFee"`
	AnnualRateBps int          `json:"annualRateBps"`
	TenureMonths  int          `json:"tenureMonths"`
	StartDate     string       `json:"startDate"`
	DisbursalDate string       `json:"disbursalDate,omitempty"`
	CreatedAt     time.Time    `json:"createdAt"`
	UpdatedAt     time.Time    `json:"updatedAt"`
}

// BackupLoanTransfer is a balance transfer between two loan accounts. It is
// exported because both loans' amortization tables are derived from it: without
// it a restored target loan would still carry the pre-transfer balance and a
// restored source loan would still owe its cancelled installments.
type BackupLoanTransfer struct {
	ID                uuid.UUID    `json:"id"`
	FromLoanAccountID uuid.UUID    `json:"fromLoanAccountId"`
	ToLoanAccountID   uuid.UUID    `json:"toLoanAccountId"`
	Amount            money.Amount `json:"amount"`
	Principal         money.Amount `json:"principal"`
	TransferDate      string       `json:"transferDate"`
	Mode              string       `json:"mode"`
	CreatedAt         time.Time    `json:"createdAt"`
}

// BackupLoanDisbursement links the bank credit that released a loan, so a
// restored loan still reconciles its disbursement against the same transaction.
type BackupLoanDisbursement struct {
	ID            uuid.UUID `json:"id"`
	LoanAccountID uuid.UUID `json:"loanAccountId"`
	TransactionID uuid.UUID `json:"transactionId"`
	CreatedAt     time.Time `json:"createdAt"`
}

// BackupRecurringSeries is one recurring series/subscription template. Its
// amount, account and date range live on its terms (BackupRecurringTerm).
type BackupRecurringSeries struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Type        string     `json:"type"`
	Frequency   string     `json:"frequency"`
	Interval    int        `json:"interval"`
	CategoryID  *uuid.UUID `json:"categoryId,omitempty"`
	PayeeID     *uuid.UUID `json:"payeeId,omitempty"`
	Active      bool       `json:"active"`
	Notes       string     `json:"notes"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

// BackupRecurringTerm is one date-ranged amount/account segment of a series.
type BackupRecurringTerm struct {
	ID        uuid.UUID    `json:"id"`
	SeriesID  uuid.UUID    `json:"seriesId"`
	StartDate string       `json:"startDate"`
	EndDate   *string      `json:"endDate,omitempty"`
	Amount    money.Amount `json:"amount"`
	AccountID uuid.UUID    `json:"accountId"`
	CreatedAt time.Time    `json:"createdAt"`
}

// BackupRecurringAttachment links a transaction to a recurring series.
type BackupRecurringAttachment struct {
	ID            uuid.UUID `json:"id"`
	SeriesID      uuid.UUID `json:"seriesId"`
	TransactionID uuid.UUID `json:"transactionId"`
	CreatedAt     time.Time `json:"createdAt"`
}

// BackupRule is one auto-categorization rule.
type BackupRule struct {
	ID         uuid.UUID  `json:"id"`
	Pattern    string     `json:"pattern"`
	MatchType  string     `json:"matchType"`
	CategoryID uuid.UUID  `json:"categoryId"`
	PayeeID    *uuid.UUID `json:"payeeId,omitempty"`
	Priority   int        `json:"priority"`
	// Conditions & extra actions (added with richer rules). All optional so a
	// bundle written before they existed still restores as a pattern-only rule.
	AccountID        *uuid.UUID    `json:"accountId,omitempty"`
	FilterCategoryID *uuid.UUID    `json:"filterCategoryId,omitempty"`
	FilterPayeeID    *uuid.UUID    `json:"filterPayeeId,omitempty"`
	MinAmount        *money.Amount `json:"minAmount,omitempty"`
	MaxAmount        *money.Amount `json:"maxAmount,omitempty"`
	TxnType          string        `json:"txnType,omitempty"`
	DateFrom         *string       `json:"dateFrom,omitempty"`
	DateTo           *string       `json:"dateTo,omitempty"`
	IsLinked         *bool         `json:"isLinked,omitempty"`
	IsRecurring      *bool         `json:"isRecurring,omitempty"`
	AddTags          []string      `json:"addTags,omitempty"`
	Notes            string        `json:"notes,omitempty"`
}

// BackupImportResult reports how many rows a restore created per resource and
// any rows it skipped. Warnings is empty when the whole bundle was applied.
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
