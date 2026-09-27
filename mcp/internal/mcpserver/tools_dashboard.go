package mcpserver

import (
	"context"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/fintrak/client/api"
	"github.com/fintrak/mcp/internal/readonly"
)

// Dashboard aggregates and the derived views (backend/handlers/dashboard.go,
// money_flow.go, money_flow_timeline.go, cash_flow_calendar.go).
//
// Every number these return is computed server-side and is the same number the
// app displays, which is the point: a model should never re-derive a total from
// a transaction page.

// windowArgs is the window shared by the dashboard tools: an inclusive date
// range, a single account, and/or a single currency.
type windowArgs struct {
	DateFrom  string `json:"dateFrom,omitempty" jsonschema:"inclusive start date, YYYY-MM-DD; omit for the server's default window"`
	DateTo    string `json:"dateTo,omitempty" jsonschema:"inclusive end date, YYYY-MM-DD"`
	AccountID string `json:"accountId,omitempty" jsonschema:"narrow the whole response to one account id"`
	Currency  string `json:"currency,omitempty" jsonschema:"narrow the whole response to the accounts holding this currency code (three letters, case-insensitive); without it every amount comes back keyed by currency"`
}

// summaryArgs adds the statement-period framing to the window.
type summaryArgs struct {
	DateFrom  string `json:"dateFrom,omitempty" jsonschema:"inclusive start date, YYYY-MM-DD"`
	DateTo    string `json:"dateTo,omitempty" jsonschema:"inclusive end date, YYYY-MM-DD"`
	AccountID string `json:"accountId,omitempty" jsonschema:"narrow to one account id, from list_accounts"`
	Currency  string `json:"currency,omitempty" jsonschema:"narrow the whole response to the accounts holding this currency code (three letters, case-insensitive); without it every amount comes back keyed by currency"`

	GroupBy string `json:"groupBy,omitempty" jsonschema:"\"billing_cycle\" to frame the response around one account's statement periods; omit for calendar months"`
	Cycles  int    `json:"cycles,omitempty" jsonschema:"how many billing cycles to span when groupBy is billing_cycle, default 12, max 60"`
}

// moneyFlowArgs adds the per-stage node cap to the window.
type moneyFlowArgs struct {
	DateFrom  string `json:"dateFrom,omitempty" jsonschema:"inclusive start date, YYYY-MM-DD"`
	DateTo    string `json:"dateTo,omitempty" jsonschema:"inclusive end date, YYYY-MM-DD"`
	AccountID string `json:"accountId,omitempty" jsonschema:"narrow the graph to one account id"`
	Currency  string `json:"currency,omitempty" jsonschema:"narrow the whole response to the accounts holding this currency code (three letters, case-insensitive); without it every amount comes back keyed by currency"`
	Limit     int    `json:"limit,omitempty" jsonschema:"cap on the income, category and payee stages, default 12, max 30; the remainder of each stage collapses into one Other node"`
}

// sideEffectBillingCycle is the disclosure shared by the aggregate tools that
// reach the billing-cycle materialization when asked to group by statement
// period. It is stated in the description the model reads (Register appends it),
// because the alternative — a blanket "read-only" claim — is untrue for these
// calls.
const sideEffectBillingCycle = "With groupBy=billing_cycle this is not a pure read: the account's missing statement periods are " +
	"generated, and its transactions are re-assigned to them, as part of answering."

// sideEffectBillingCycleForAccount is the same disclosure for a tool that only
// reaches it when a single account is selected.
const sideEffectBillingCycleForAccount = "With a single accountId this is not a pure read: that account's missing statement " +
	"periods are generated, and its transactions are re-assigned to them, as part of answering."

func dashboardTools() []Tool {
	return []Tool{
		{
			Name:  "get_dashboard_summary",
			Title: "Get the dashboard summary",
			Description: "The app's dashboard aggregate: account and transaction counts, income, expense and net — each per " +
				"currency, so a single-currency window carries one entry and a mixed one carries several — per-category spend " +
				"and income (top 15 each), a trend series and the most recent transactions. With groupBy=billing_cycle the " +
				"whole response is framed around one account's statement periods and accountId is required. totalNet is the " +
				"server's own per-currency difference; do not derive it by subtracting totalExpense from totalIncome. Prefer " +
				"this to summing a transaction page. " + perCurrencyAmounts + narrowByCurrency,
			SideEffect: sideEffectBillingCycle,
			Route:      readonly.Route{Method: http.MethodGet, Path: "/dashboard/summary"},
			install:    installDashboardSummary,
		},
		{
			Name:  "get_money_flow",
			Title: "Get the money-flow graph",
			Description: "The Sankey graph behind the Money Flow page: income sources to accounts to categories to payees, with " +
				"cross-account transfer/refund/cashback/bill-payment links as real account-to-account edges (already cycle-free) and a " +
				"per-link-type rollup that is not drawn. Every node, edge and rollup total carries the flow through it keyed by " +
				"currency, so a node fed by two currencies has no single total and the API does not invent one. A node's total is " +
				"the server's own figure — an account node's is the larger of its inflow and outflow — so read it rather than adding " +
				"up the edges around it. " + perCurrencyAmounts + narrowByCurrency,
			Route:   readonly.Route{Method: http.MethodGet, Path: "/dashboard/money-flow"},
			install: installMoneyFlow,
		},
		{
			Name:  "get_money_flow_timeline",
			Title: "Get the money-flow timeline",
			Description: "One period of income, expense and net per calendar month, or per billing cycle when groupBy=billing_cycle " +
				"(which needs accountId) — every figure keyed by currency. A net is a difference taken within one currency and is " +
				"never one taken across two, so read the period's own net instead of subtracting its income from its expense. Each " +
				"period carries its inclusive start and end dates, so a period can be handed straight back to get_money_flow as " +
				"dateFrom/dateTo. " + perCurrencyAmounts + narrowByCurrency,
			SideEffect: sideEffectBillingCycle,
			Route:      readonly.Route{Method: http.MethodGet, Path: "/dashboard/money-flow/timeline"},
			install:    installMoneyFlowTimeline,
		},
		{
			Name:  "get_cash_flow_calendar",
			Title: "Get the cash-flow calendar",
			Description: "Daily income, expense and net for every day in the window that has transactions, with window totals " +
				"per currency. maxAbsNet is the largest absolute daily net per currency, and it is the scale the heatmap is drawn " +
				"against, so one day's size is comparable to another's only within a single currency. Selecting a single account also " +
				"returns that account's billing-cycle boundaries and its synthetic summary markers " +
				"(month-end running balances, or per-cycle total outstanding), which are overlay data and excluded from the day totals. " +
				perCurrencyAmounts + narrowByCurrency,
			SideEffect: sideEffectBillingCycleForAccount,
			Route:      readonly.Route{Method: http.MethodGet, Path: "/dashboard/cash-flow-calendar"},
			install:    installCashFlowCalendar,
		},
	}
}

func installDashboardSummary(s *mcp.Server, c *api.Client, tool *mcp.Tool) {
	addReadTool(s, tool, func(ctx context.Context, in summaryArgs) (any, error) {
		return c.Summary(ctx, api.DashboardFilter{
			WindowFilter: api.WindowFilter{
				DateFrom:  in.DateFrom,
				DateTo:    in.DateTo,
				AccountID: in.AccountID,
				Currency:  in.Currency,
			},
			GroupBy: in.GroupBy,
			Cycles:  in.Cycles,
		})
	})
}

func installMoneyFlow(s *mcp.Server, c *api.Client, tool *mcp.Tool) {
	addReadTool(s, tool, func(ctx context.Context, in moneyFlowArgs) (any, error) {
		return c.MoneyFlow(ctx, api.MoneyFlowFilter{
			WindowFilter: api.WindowFilter{
				DateFrom:  in.DateFrom,
				DateTo:    in.DateTo,
				AccountID: in.AccountID,
				Currency:  in.Currency,
			},
			Limit: in.Limit,
		})
	})
}

func installMoneyFlowTimeline(s *mcp.Server, c *api.Client, tool *mcp.Tool) {
	addReadTool(s, tool, func(ctx context.Context, in summaryArgs) (any, error) {
		return c.MoneyFlowTimeline(ctx, api.TimelineFilter{
			WindowFilter: api.WindowFilter{
				DateFrom:  in.DateFrom,
				DateTo:    in.DateTo,
				AccountID: in.AccountID,
				Currency:  in.Currency,
			},
			GroupBy: in.GroupBy,
			Cycles:  in.Cycles,
		})
	})
}

func installCashFlowCalendar(s *mcp.Server, c *api.Client, tool *mcp.Tool) {
	addReadTool(s, tool, func(ctx context.Context, in windowArgs) (any, error) {
		return c.CashFlowCalendar(ctx, api.WindowFilter{
			DateFrom:  in.DateFrom,
			DateTo:    in.DateTo,
			AccountID: in.AccountID,
			Currency:  in.Currency,
		})
	})
}
