// Package models defines the shared request, response, and persistence types
// used across the FinTrak API. JSON tags mirror the shapes the frontend expects,
// and optional-field types (OptionalUUID, OptionalInt) encode the difference
// between "key absent" and "key explicitly null" on partial updates.
package models

import (
	"encoding/json"
	"time"

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
// users; the built-in "bank" and "credit_card" types are immutable.
type AccountType struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	PositiveTxnType string `json:"positiveTxnType"`
}

// Account is a user's bank account or credit card.
type Account struct {
	ID              uuid.UUID `json:"id"`
	Name            string    `json:"name"`
	AccountTypeID   string    `json:"accountTypeId"`
	AccountTypeName string    `json:"accountTypeName,omitempty"`
	Bank            string    `json:"bank"`
	Currency        string    `json:"currency"`
	Color           string    `json:"color"`
	IsDefault       bool      `json:"isDefault"`
	Balance         float64   `json:"balance"`
	CreatedAt       time.Time `json:"createdAt"`
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
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Icon     string     `json:"icon"`
	Color    string     `json:"color"`
	IsBase   bool       `json:"isBase"`
	IsGlobal bool       `json:"isGlobal"`
	UserID   *uuid.UUID `json:"userId,omitempty"`
	SortOrder int       `json:"sortOrder"`
}

// Category is a user-scoped (or global) grouping for transactions. GroupID
// references a CategoryGroup ("income", "expense", "transfer", "cashback", or a
// user's custom group).
type Category struct {
	ID       uuid.UUID  `json:"id"`
	Name     string     `json:"name"`
	Icon     string     `json:"icon"`
	Color    string     `json:"color"`
	GroupID  string     `json:"groupId"`
	IsGlobal bool       `json:"isGlobal"`
	// Joined
	GroupName   string `json:"groupName,omitempty"`
	GroupIsBase bool   `json:"groupIsBase,omitempty"`
}

// Transaction is a single debit or credit entry on an account. The joined
// fields (AccountName, CategoryName, ...) are populated by read queries and are
// absent on writes.
type Transaction struct {
	ID          uuid.UUID  `json:"id"`
	AccountID   uuid.UUID  `json:"accountId"`
	Date        time.Time  `json:"date"`
	Description string     `json:"description"`
	Amount      float64    `json:"amount"`
	Type        string     `json:"type"`
	CategoryID  *uuid.UUID `json:"categoryId"`
	Tags        []string   `json:"tags"`
	Notes       string     `json:"notes"`
	PayeeID     *uuid.UUID `json:"payeeId,omitempty"`
	Payee       string     `json:"payee"`
	CreatedAt   time.Time  `json:"createdAt"`
	// Joined fields
	AccountName   string     `json:"accountName,omitempty"`
	CategoryName  string     `json:"categoryName,omitempty"`
	CategoryIcon  string     `json:"categoryIcon,omitempty"`
	CategoryColor string     `json:"categoryColor,omitempty"`
	IsLinked      bool       `json:"isLinked"`
	LinkCount     int        `json:"linkCount"`
	LinkID        *uuid.UUID `json:"linkId,omitempty"`
	IsSummary     bool       `json:"isSummary,omitempty"`
	// Billing cycle attachment (credit cards)
	BillingCycleID    *uuid.UUID `json:"billingCycleId,omitempty"`
	BillingCycleLabel string     `json:"billingCycleLabel,omitempty"`
}

// BillingCycle is a persisted billing period for an account with a billing day
// set. Cycles are auto-generated from the account's billing day; transactions
// are attached to them via Transaction.BillingCycleID. TotalOutstanding is the
// sum of the debit (purchase) transactions attached to the cycle.
type BillingCycle struct {
	ID               uuid.UUID `json:"id"`
	AccountID        uuid.UUID `json:"accountId"`
	StartDate        time.Time `json:"startDate"`
	EndDate          time.Time `json:"endDate"`
	Label            string    `json:"label"`
	TotalOutstanding float64   `json:"totalOutstanding"`
	TransactionCount int       `json:"transactionCount"`
}

// Rule automatically assigns a category (and optionally a payee) to a
// transaction whose description matches Pattern. Rules are evaluated in
// priority order (highest first) during transaction creation, imports, and
// ApplyRules.
type Rule struct {
	ID         uuid.UUID  `json:"id"`
	Pattern    string     `json:"pattern"`
	MatchType  string     `json:"matchType"`
	CategoryID uuid.UUID  `json:"categoryId"`
	PayeeID    *uuid.UUID `json:"payeeId,omitempty"`
	Payee      string     `json:"payee"`
	Priority   int        `json:"priority"`
	// Joined
	CategoryName string `json:"categoryName,omitempty"`
}

// Link pairs two transactions that belong together — typically a transfer
// between the user's own accounts, or a cashback/refund that corresponds to an
// earlier purchase. FromTxn/ToTxn carry the joined transaction details.
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

// PaperlessImportRequest asks the backend to pull a single Paperless document,
// parse it with the statement parser, and return the normalized transactions.
type PaperlessImportRequest struct {
	DocumentID int    `json:"documentId" binding:"required"`
	Extractor  string `json:"extractor"`
	Password   string `json:"password"`
	DateFormat string `json:"dateFormat"`
}

// RegisterRequest is the body for POST /api/v1/auth/register.
type RegisterRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
}

// LoginRequest is the body for POST /api/v1/auth/login.
type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
}

// AuthResponse is returned by the register and login endpoints.
type AuthResponse struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

// CreateAccountRequest is the body for POST /api/v1/accounts.
type CreateAccountRequest struct {
	Name          string `json:"name" binding:"required"`
	AccountTypeID string `json:"accountTypeId" binding:"required"`
	Bank          string `json:"bank"`
	Currency      string `json:"currency"`
	Color         string `json:"color"`
	IsDefault     bool   `json:"isDefault"`
	// BillingDay is the day of the month on which billing cycles end (1-31).
	// Optional: when set, summary rows are generated for the account regardless
	// of its type; omitted stores NULL (no cycles, no summary rows).
	BillingDay *int `json:"billingDay" binding:"omitempty,min=1,max=31"`
}

// UpdateAccountRequest uses a *bool for IsDefault so that an absent key leaves
// the current default flag untouched (the edit form does not send it), while an
// explicit true/false sets it.
type UpdateAccountRequest struct {
	Name          string `json:"name"`
	AccountTypeID string `json:"accountTypeId"`
	Bank          string `json:"bank"`
	Currency      string `json:"currency"`
	Color         string `json:"color"`
	IsDefault     *bool  `json:"isDefault"`
	// BillingDay is the day of the month on which billing cycles end (1-31).
	// Optional: omitted leaves the current value untouched, an explicit value
	// sets it, and null clears it.
	BillingDay *int `json:"billingDay" binding:"omitempty,min=1,max=31"`
}

// CreateCategoryRequest is the body for POST /api/v1/categories.
type CreateCategoryRequest struct {
	Name    string `json:"name" binding:"required"`
	Icon    string `json:"icon"`
	Color   string `json:"color"`
	GroupID string `json:"groupId" binding:"required"`
}

// UpdateCategoryRequest is the body for PUT /api/v1/categories/:id.
type UpdateCategoryRequest struct {
	Name    string `json:"name"`
	Icon    string `json:"icon"`
	Color   string `json:"color"`
	GroupID string `json:"groupId"`
}

// DeleteCategoryResult reports the side effects of deleting a category: how
// many transactions were uncategorized and how many rules were removed.
type DeleteCategoryResult struct {
	ClearedTransactions int `json:"clearedTransactions"`
	DeletedRules        int `json:"deletedRules"`
}

// CreateCategoryGroupRequest is the body for POST /api/v1/groups.
type CreateCategoryGroupRequest struct {
	ID    string `json:"id" binding:"required"`
	Name  string `json:"name" binding:"required"`
	Icon  string `json:"icon"`
	Color string `json:"color"`
}

// UpdateCategoryGroupRequest is the body for PUT /api/v1/groups/:id.
type UpdateCategoryGroupRequest struct {
	Name  string `json:"name"`
	Icon  string `json:"icon"`
	Color string `json:"color"`
}

// CreateAccountTypeRequest is the admin-only body for POST /api/v1/account-types.
type CreateAccountTypeRequest struct {
	ID              string `json:"id" binding:"required"`
	Name            string `json:"name" binding:"required"`
	PositiveTxnType string `json:"positiveTxnType" binding:"required"`
}

// UpdateAccountTypeRequest is the admin-only body for PUT /api/v1/account-types/:id.
type UpdateAccountTypeRequest struct {
	Name            string `json:"name"`
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
	CategoryID     OptionalUUID `json:"categoryId"`
	Tags           *[]string    `json:"tags"`
	Notes          *string      `json:"notes"`
	PayeeID        OptionalUUID `json:"payeeId"`
	Date           *string      `json:"date"`
	Description    *string      `json:"description"`
	Amount         *float64     `json:"amount"`
	Type           *string      `json:"type"`
	AccountID      *uuid.UUID   `json:"accountId"`
	BillingCycleID OptionalUUID `json:"billingCycleId"`
}

// CreateTransactionRequest is the body for POST /api/v1/transactions.
type CreateTransactionRequest struct {
	AccountID      uuid.UUID  `json:"accountId" binding:"required"`
	Date           string     `json:"date" binding:"required"`
	Description    string     `json:"description" binding:"required"`
	Amount         float64    `json:"amount" binding:"required"`
	Type           string     `json:"type" binding:"required"`
	CategoryID     *uuid.UUID `json:"categoryId"`
	PayeeID        *uuid.UUID `json:"payeeId"`
	Tags           []string   `json:"tags"`
	Notes          string     `json:"notes"`
	BillingCycleID *uuid.UUID `json:"billingCycleId"`
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

// ImportRequest is the body for POST /api/v1/transactions/import. DuplicateAction
// is "skip" (drop rows that already exist) or "keep" (insert everything);
// BillingCycleID attaches credit-card imports to a specific cycle; and
// PaperlessDocumentIDs are tagged after a successful import.
type ImportRequest struct {
	AccountID            uuid.UUID           `json:"accountId"`
	Transactions         []ImportTransaction `json:"transactions"`
	DuplicateAction      string              `json:"duplicateAction"`      // "skip" | "keep"
	BillingCycleID       *uuid.UUID          `json:"billingCycleId"`       // credit-card imports: attach every imported transaction to this cycle
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
	Index       int     `json:"index"`
	Exists      bool    `json:"exists"`
	Date        string  `json:"date"`
	Description string  `json:"description"`
	Amount      float64 `json:"amount"`
	Type        string  `json:"type"`
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
	Date        string     `json:"date"`
	Description string     `json:"description"`
	Amount      float64    `json:"amount"`
	Type        string     `json:"type"`
	PayeeID     *uuid.UUID `json:"payeeId"`
}

// CreateRuleRequest is the body for POST /api/v1/rules.
type CreateRuleRequest struct {
	Pattern    string     `json:"pattern" binding:"required"`
	MatchType  string     `json:"matchType"`
	CategoryID uuid.UUID  `json:"categoryId" binding:"required"`
	PayeeID    *uuid.UUID `json:"payeeId"`
	Priority   int        `json:"priority"`
}

// UpdateRuleRequest is the body for PUT /api/v1/rules/:id.
type UpdateRuleRequest struct {
	Pattern    string     `json:"pattern"`
	MatchType  string     `json:"matchType"`
	CategoryID uuid.UUID  `json:"categoryId"`
	PayeeID    *uuid.UUID `json:"payeeId"`
	Priority   int        `json:"priority"`
}

// CreatePayeeRequest is the body for POST /api/v1/payees.
type CreatePayeeRequest struct {
	Name      string     `json:"name" binding:"required"`
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
type DashboardSummary struct {
	TotalAccounts      int             `json:"totalAccounts"`
	TotalTransactions  int             `json:"totalTransactions"`
	TotalIncome        float64         `json:"totalIncome"`
	TotalExpense       float64         `json:"totalExpense"`
	ByCategory         []CategorySpend `json:"byCategory"`
	IncomeByCategory   []CategorySpend `json:"incomeByCategory"`
	MonthlyTrend       []MonthlyData   `json:"monthlyTrend"`
	RecentTransactions []Transaction   `json:"recentTransactions"`
}

// CategorySpend aggregates spend/income for a single category.
type CategorySpend struct {
	CategoryID    uuid.UUID `json:"categoryId"`
	CategoryName  string    `json:"categoryName"`
	CategoryColor string    `json:"categoryColor"`
	CategoryIcon  string    `json:"categoryIcon"`
	Total         float64   `json:"total"`
	Count         int       `json:"count"`
}

// MonthlyData holds income and expense totals for one month (keyed "YYYY-MM").
type MonthlyData struct {
	Month   string  `json:"month"`
	Income  float64 `json:"income"`
	Expense float64 `json:"expense"`
}

// TransferSuggestion proposes that two transactions be linked, e.g. a debit and
// a matching credit in different accounts. Score (0-100) estimates how
// confident the suggestion is.
type TransferSuggestion struct {
	DebitTxn  Transaction `json:"debitTxn"`
	CreditTxn Transaction `json:"creditTxn"`
	Score     float64     `json:"score"`
}
