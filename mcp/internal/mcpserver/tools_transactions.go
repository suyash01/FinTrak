package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/fintrak/client/api"
	"github.com/fintrak/mcp/internal/readonly"
)

// Transactions: the ledger itself, plus the import duplicate check
// (backend/handlers/transaction.go, transaction_import.go).

// listTransactionsArgs is the transaction query grammar. It mirrors
// api.TransactionFilter, which mirrors the app's own filter builder, so the
// model can reach every slice of the ledger the UI can.
type listTransactionsArgs struct {
	AccountID  string `json:"accountId,omitempty" jsonschema:"narrow to one account id, from list_accounts; a single account also returns the synthetic summary rows (running balance or cycle outstanding) with isSummary set"`
	CategoryID string `json:"categoryId,omitempty" jsonschema:"a category id from list_categories, the sentinel \"uncategorized\", or a category group id/slug"`
	GroupID    string `json:"groupId,omitempty" jsonschema:"match every category in this group id, from list_groups"`
	Search     string `json:"search,omitempty" jsonschema:"case-insensitive substring of description, notes, payee name or tags"`

	Type string `json:"type,omitempty" jsonschema:"\"debit\" or \"credit\""`
	// PayeeID takes the sentinel "none" for transactions without a payee.
	PayeeID string `json:"payeeId,omitempty" jsonschema:"a payee id from list_payees, or the sentinel \"none\" for transactions without one"`

	Amount   string `json:"amount,omitempty" jsonschema:"match an exact amount, in major units, e.g. \"1250.50\""`
	DateFrom string `json:"dateFrom,omitempty" jsonschema:"inclusive start date, YYYY-MM-DD"`
	DateTo   string `json:"dateTo,omitempty" jsonschema:"inclusive end date, YYYY-MM-DD"`

	Tags            []string `json:"tags,omitempty" jsonschema:"keep transactions carrying ANY of these tags"`
	Linked          *bool    `json:"linked,omitempty" jsonschema:"true for linked transactions, false for unlinked; omit to leave unfiltered"`
	Uncategorized   bool     `json:"uncategorized,omitempty" jsonschema:"keep only transactions without a category"`
	LoanAccountID   string   `json:"loanAccountId,omitempty" jsonschema:"keep only transactions attached to this loan/EMI account"`
	ExcludeAttached bool     `json:"excludeAttached,omitempty" jsonschema:"drop transactions already attached to a loan account; recurring-series attachments are not excluded"`
	RecurringID     string   `json:"recurringId,omitempty" jsonschema:"keep only transactions attached to this recurring series id"`
	Recurring       string   `json:"recurring,omitempty" jsonschema:"\"linked\" or \"unlinked\" recurring state"`

	SortBy    string `json:"sortBy,omitempty" jsonschema:"date, amount or createdAt"`
	SortOrder string `json:"sortOrder,omitempty" jsonschema:"ASC or DESC (default DESC)"`
	Page      int    `json:"page,omitempty" jsonschema:"page number, default 1"`
	Limit     int    `json:"limit,omitempty" jsonschema:"page size, default 50, max 1000"`
}

// transactionRow is one candidate row for the duplicate check.
type transactionRow struct {
	Date        string `json:"date" jsonschema:"transaction date, YYYY-MM-DD"`
	Description string `json:"description" jsonschema:"the row's description text"`
	Amount      string `json:"amount" jsonschema:"amount in major units, e.g. \"1250.50\""`
	Type        string `json:"type" jsonschema:"\"debit\" or \"credit\""`
	PayeeID     string `json:"payeeId,omitempty" jsonschema:"optional payee id from list_payees"`
}

// validateTransactionsArgs asks whether rows would be new or duplicates.
type validateTransactionsArgs struct {
	AccountID    string           `json:"accountId" jsonschema:"the account the rows would be imported into, from list_accounts"`
	Transactions []transactionRow `json:"transactions" jsonschema:"the candidate rows to check against what the account already holds"`
}

func transactionTools() []Tool {
	return []Tool{
		{
			Name:  "list_transactions",
			Title: "List transactions",
			Description: "One page of transactions, newest first by default, with the same filters the app's transaction list offers " +
				"(account, category or group, payee, tags, amount, date range, type, linked, recurring, attachment state, " +
				"uncategorized). Amounts are the ledger's own decimal values; totals are not computed here — use " +
				"get_dashboard_summary or list_billing_cycles for aggregates.",
			SideEffect: "With an accountId and the default date sort this is not a pure read: that account's missing billing " +
				"cycles are generated, and its transactions' cycle assignments back-filled, as part of answering.",
			Route:   readonly.Route{Method: http.MethodGet, Path: "/transactions"},
			install: installListTransactions,
		},
		{
			Name:  "validate_transactions",
			Title: "Check rows for duplicates",
			Description: "The read-only half of the import path: reports, row by row, whether each candidate transaction already exists " +
				"in the account (same date, amount, type and description fingerprint). It writes nothing, and it is the honest way to " +
				"answer \"has this charge already been recorded?\".",
			Route:   readonly.Route{Method: http.MethodPost, Path: "/transactions/validate"},
			install: installValidateTransactions,
		},
	}
}

func installListTransactions(s *mcp.Server, c *api.Client, tool *mcp.Tool) {
	addReadTool(s, tool, func(ctx context.Context, in listTransactionsArgs) (any, error) {
		return c.ListTransactions(ctx, api.TransactionFilter{
			AccountID:       in.AccountID,
			CategoryID:      in.CategoryID,
			GroupID:         in.GroupID,
			Search:          in.Search,
			Type:            in.Type,
			PayeeID:         in.PayeeID,
			Amount:          in.Amount,
			DateFrom:        in.DateFrom,
			DateTo:          in.DateTo,
			Tags:            in.Tags,
			Linked:          in.Linked,
			Uncategorized:   in.Uncategorized,
			LoanAccountID:   in.LoanAccountID,
			ExcludeAttached: in.ExcludeAttached,
			RecurringID:     in.RecurringID,
			Recurring:       in.Recurring,
			SortBy:          in.SortBy,
			SortOrder:       in.SortOrder,
			Page:            in.Page,
			Limit:           in.Limit,
		})
	})
}

func installValidateTransactions(s *mcp.Server, c *api.Client, tool *mcp.Tool) {
	addReadTool(s, tool, func(ctx context.Context, in validateTransactionsArgs) (any, error) {
		rows := make([]api.ImportTransaction, 0, len(in.Transactions))
		for i, row := range in.Transactions {
			amount, err := parseAmount(row.Amount)
			if err != nil {
				return nil, fmt.Errorf("transactions[%d]: %w", i, err)
			}
			entry := api.ImportTransaction{
				Date:        row.Date,
				Description: row.Description,
				Amount:      amount,
				Type:        row.Type,
			}
			if row.PayeeID != "" {
				entry.PayeeID = new(row.PayeeID)
			}
			rows = append(rows, entry)
		}
		return c.ValidateTransactions(ctx, api.ValidateTransactionsRequest{
			AccountID:    in.AccountID,
			Transactions: rows,
		})
	})
}

// parseAmount validates an amount a model supplied, using the same grammar the
// API and the app's own inputs use (at most two decimal places). An empty value
// stays empty, which callers read as "absent".
func parseAmount(s string) (api.Amount, error) {
	if strings.TrimSpace(s) == "" {
		return "", nil
	}
	amount, err := api.ParseAmount(s)
	if err != nil {
		return "", fmt.Errorf("invalid amount %q: %w", s, err)
	}
	return amount, nil
}
