package models

import (
	"time"

	"github.com/google/uuid"
)

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
	Hash        string     `json:"hash"`
	CreatedAt   time.Time  `json:"createdAt"`
	// Joined fields
	AccountName   string     `json:"accountName,omitempty"`
	CategoryName  string     `json:"categoryName,omitempty"`
	CategoryIcon  string     `json:"categoryIcon,omitempty"`
	CategoryColor string     `json:"categoryColor,omitempty"`
	IsLinked      bool       `json:"isLinked"`
	LinkID        *uuid.UUID `json:"linkId,omitempty"`
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

type CreateAccountRequest struct {
	Name          string `json:"name" binding:"required"`
	AccountTypeID string `json:"accountTypeId" binding:"required"`
	Bank          string `json:"bank"`
	Currency      string `json:"currency"`
	Color         string `json:"color"`
}

type UpdateAccountRequest struct {
	Name          string `json:"name"`
	AccountTypeID string `json:"accountTypeId"`
	Bank          string `json:"bank"`
	Currency      string `json:"currency"`
	Color         string `json:"color"`
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

type UpdateTransactionRequest struct {
	CategoryID  *uuid.UUID `json:"categoryId"`
	Tags        []string   `json:"tags"`
	Notes       string     `json:"notes"`
	PayeeID     *uuid.UUID `json:"payeeId"`
	Date        *string    `json:"date"`
	Description *string    `json:"description"`
	Amount      *float64   `json:"amount"`
	Type        *string    `json:"type"`
	AccountID   *uuid.UUID `json:"accountId"`
}

type BulkCategorizeRequest struct {
	TransactionIDs []uuid.UUID `json:"transactionIds" binding:"required"`
	CategoryID     uuid.UUID   `json:"categoryId" binding:"required"`
}

type BulkUpdatePayeeRequest struct {
	TransactionIDs []uuid.UUID `json:"transactionIds" binding:"required"`
	PayeeID        uuid.UUID   `json:"payeeId" binding:"required"`
}

type BulkDeleteTransactionsRequest struct {
	TransactionIDs []uuid.UUID `json:"transactionIds" binding:"required"`
}

type ImportRequest struct {
	AccountID    uuid.UUID           `json:"accountId"`
	Transactions []ImportTransaction `json:"transactions"`
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
