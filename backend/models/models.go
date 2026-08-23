package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type FieldError struct {
	Field   string `json:"field,omitempty"`
	Tag     string `json:"tag,omitempty"`
	Message string `json:"message"`
}

type ErrorResponse struct {
	Errors []FieldError `json:"errors"`
}

type AccountType struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	PositiveTxnType string `json:"positiveTxnType"`
}

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
}

type Payee struct {
	ID        uuid.UUID  `json:"id"`
	Name      string     `json:"name"`
	AccountID *uuid.UUID `json:"accountId,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

type Category struct {
	ID       uuid.UUID  `json:"id"`
	Name     string     `json:"name"`
	Icon     string     `json:"icon"`
	Color    string     `json:"color"`
	ParentID *uuid.UUID `json:"parentId"`
	Type     string     `json:"type"`
}

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

// BillingCycle is a persisted billing period for a credit-card account. Cycles
// are auto-generated from the account's billing day; transactions are attached
// to them via Transaction.BillingCycleID. TotalOutstanding is the sum of the
// debit (purchase) transactions attached to the cycle.
type BillingCycle struct {
	ID               uuid.UUID `json:"id"`
	AccountID        uuid.UUID `json:"accountId"`
	StartDate        time.Time `json:"startDate"`
	EndDate          time.Time `json:"endDate"`
	Label            string    `json:"label"`
	TotalOutstanding float64   `json:"totalOutstanding"`
	TransactionCount int       `json:"transactionCount"`
}

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

type User struct {
	ID    uuid.UUID `json:"id"`
	Email string    `json:"email"`
}

// UserSettings holds per-user integration configuration, stored against the
// user row rather than in docker/env config.
type UserSettings struct {
	PaperlessURL   string `json:"paperlessUrl"`
	PaperlessToken string `json:"paperlessToken"`
	PaperlessTag   string `json:"paperlessTag"`
	PageSize       *int   `json:"pageSize"`
}

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

type PaperlessImportRequest struct {
	DocumentID int    `json:"documentId" binding:"required"`
	Extractor  string `json:"extractor"`
	Password   string `json:"password"`
	DateFormat string `json:"dateFormat"`
}

type RegisterRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
}

type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
}

type AuthResponse struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

type CreateAccountRequest struct {
	Name          string `json:"name" binding:"required"`
	AccountTypeID string `json:"accountTypeId" binding:"required"`
	Bank          string `json:"bank"`
	Currency      string `json:"currency"`
	Color         string `json:"color"`
	IsDefault     bool   `json:"isDefault"`
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
}

type CreateAccountTypeRequest struct {
	ID              string `json:"id" binding:"required"`
	Name            string `json:"name" binding:"required"`
	PositiveTxnType string `json:"positiveTxnType" binding:"required"`
}

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

type BulkCategorizeRequest struct {
	TransactionIDs []uuid.UUID `json:"transactionIds" binding:"required"`
	CategoryID     uuid.UUID   `json:"categoryId" binding:"required"`
}

type BulkUpdatePayeeRequest struct {
	TransactionIDs []uuid.UUID `json:"transactionIds" binding:"required"`
	PayeeID        uuid.UUID   `json:"payeeId" binding:"required"`
}

type BulkBillingCycleRequest struct {
	TransactionIDs []uuid.UUID `json:"transactionIds" binding:"required"`
	BillingCycleID uuid.UUID   `json:"billingCycleId" binding:"required"`
}

type BulkDeleteTransactionsRequest struct {
	TransactionIDs []uuid.UUID `json:"transactionIds" binding:"required"`
}

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

type ValidateTransactionsResponse struct {
	Total         int                         `json:"total"`
	ExistingCount int                         `json:"existingCount"`
	MissingCount  int                         `json:"missingCount"`
	Results       []ValidateTransactionResult `json:"results"`
}

type ImportTransaction struct {
	Date        string     `json:"date"`
	Description string     `json:"description"`
	Amount      float64    `json:"amount"`
	Type        string     `json:"type"`
	PayeeID     *uuid.UUID `json:"payeeId"`
}

type CreateRuleRequest struct {
	Pattern    string     `json:"pattern" binding:"required"`
	MatchType  string     `json:"matchType"`
	CategoryID uuid.UUID  `json:"categoryId" binding:"required"`
	PayeeID    *uuid.UUID `json:"payeeId"`
	Priority   int        `json:"priority"`
}

type UpdateRuleRequest struct {
	Pattern    string     `json:"pattern"`
	MatchType  string     `json:"matchType"`
	CategoryID uuid.UUID  `json:"categoryId"`
	PayeeID    *uuid.UUID `json:"payeeId"`
	Priority   int        `json:"priority"`
}

type CreatePayeeRequest struct {
	Name      string     `json:"name" binding:"required"`
	AccountID *uuid.UUID `json:"accountId"`
}

type CreateLinkRequest struct {
	Type      string    `json:"type" binding:"required"`
	FromTxnID uuid.UUID `json:"fromTxnId" binding:"required"`
	ToTxnID   uuid.UUID `json:"toTxnId" binding:"required"`
	Notes     string    `json:"notes"`
}

type BulkCreateLinksRequest struct {
	Links []CreateLinkRequest `json:"links" binding:"required"`
}

type BulkDeleteLinksRequest struct {
	IDs []uuid.UUID `json:"ids" binding:"required"`
}

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

type CategorySpend struct {
	CategoryID    uuid.UUID `json:"categoryId"`
	CategoryName  string    `json:"categoryName"`
	CategoryColor string    `json:"categoryColor"`
	CategoryIcon  string    `json:"categoryIcon"`
	Total         float64   `json:"total"`
	Count         int       `json:"count"`
}

type MonthlyData struct {
	Month   string  `json:"month"`
	Income  float64 `json:"income"`
	Expense float64 `json:"expense"`
}

type TransferSuggestion struct {
	DebitTxn  Transaction `json:"debitTxn"`
	CreditTxn Transaction `json:"creditTxn"`
	Score     float64     `json:"score"`
}
