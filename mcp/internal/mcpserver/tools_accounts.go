package mcpserver

import (
	"context"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/fintrak/client/api"
	"github.com/fintrak/mcp/internal/readonly"
)

// Accounts, account types, billing cycles and loan schedules
// (backend/handlers/account.go, account_type.go, billing_cycle.go, loan.go).

// accountIDArgs is the input of a tool that operates on one account.
type accountIDArgs struct {
	AccountID string `json:"accountId" jsonschema:"the account's id, from list_accounts"`
}

// loanPayoffArgs quotes settling a loan on a date.
type loanPayoffArgs struct {
	AccountID string `json:"accountId" jsonschema:"the loan account's id, from list_accounts"`
	Date      string `json:"date" jsonschema:"the settlement date to quote, YYYY-MM-DD"`
}

func accountTools() []Tool {
	return []Tool{
		{
			Name:  "list_accounts",
			Title: "List accounts",
			Description: "Every account the user owns, default first, with its type, currency, billing day and current balance. " +
				"Start here: the account ids every other tool takes come from this list.",
			Route:   readonly.Route{Method: http.MethodGet, Path: "/accounts"},
			install: installListAccounts,
		},
		{
			Name:  "list_account_types",
			Title: "List account types",
			Description: "The account types the ledger knows (bank, credit card, loan/EMI, cash and any the user added), " +
				"with their ids and whether each carries a billing day.",
			Route:   readonly.Route{Method: http.MethodGet, Path: "/account-types"},
			install: installListAccountTypes,
		},
		{
			Name:  "list_billing_cycles",
			Title: "List billing cycles",
			Description: "One account's statement periods, newest first, each with its total outstanding and whether it is closed. " +
				"Accounts without a billing day return an empty list.",
			SideEffect: "Not a pure read: the account's missing statement periods are generated, and its transactions are " +
				"re-assigned to them, as part of answering.",
			Route:   readonly.Route{Method: http.MethodGet, Path: "/accounts/{id}/billing-cycles"},
			install: installListBillingCycles,
		},
		{
			Name:  "get_loan_schedule",
			Title: "Get loan schedule",
			Description: "A loan/EMI account's amortization table: every installment split into principal and interest with its " +
				"remaining balance, plus the EMI, total interest and progress derived from the EMI payments already attached. " +
				"A loan without terms answers with a null schedule rather than an error.",
			Route:   readonly.Route{Method: http.MethodGet, Path: "/accounts/{id}/loan-schedule"},
			install: installGetLoanSchedule,
		},
		{
			Name:  "get_loan_payoff",
			Title: "Quote a loan payoff",
			Description: "What settling a loan costs on a given date: its outstanding principal plus the interest accrued since the " +
				"last EMI payment. Requires a loan that has a schedule and has not already been settled by a balance transfer.",
			Route:   readonly.Route{Method: http.MethodGet, Path: "/accounts/{id}/loan-payoff"},
			install: installGetLoanPayoff,
		},
	}
}

func installListAccounts(s *mcp.Server, c *api.Client, tool *mcp.Tool) {
	addReadTool(s, tool, func(ctx context.Context, _ noArgs) (any, error) {
		return c.ListAccounts(ctx)
	})
}

func installListAccountTypes(s *mcp.Server, c *api.Client, tool *mcp.Tool) {
	addReadTool(s, tool, func(ctx context.Context, _ noArgs) (any, error) {
		return c.ListAccountTypes(ctx)
	})
}

func installListBillingCycles(s *mcp.Server, c *api.Client, tool *mcp.Tool) {
	addReadTool(s, tool, func(ctx context.Context, in accountIDArgs) (any, error) {
		return c.ListBillingCycles(ctx, in.AccountID)
	})
}

func installGetLoanSchedule(s *mcp.Server, c *api.Client, tool *mcp.Tool) {
	addReadTool(s, tool, func(ctx context.Context, in accountIDArgs) (any, error) {
		return c.LoanSchedule(ctx, in.AccountID)
	})
}

func installGetLoanPayoff(s *mcp.Server, c *api.Client, tool *mcp.Tool) {
	addReadTool(s, tool, func(ctx context.Context, in loanPayoffArgs) (any, error) {
		return c.LoanPayoff(ctx, in.AccountID, in.Date)
	})
}
