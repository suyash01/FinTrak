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
// range and/or a single account.
type windowArgs struct {
	DateFrom  string `json:"dateFrom,omitempty" jsonschema:"inclusive start date, YYYY-MM-DD; omit for the server's default window"`
	DateTo    string `json:"dateTo,omitempty" jsonschema:"inclusive end date, YYYY-MM-DD"`
	AccountID string `json:"accountId,omitempty" jsonschema:"narrow the whole response to one account id"`
}

// summaryArgs adds the statement-period framing to the window.
type summaryArgs struct {
	DateFrom  string `json:"dateFrom,omitempty" jsonschema:"inclusive start date, YYYY-MM-DD"`
	DateTo    string `json:"dateTo,omitempty" jsonschema:"inclusive end date, YYYY-MM-DD"`
	AccountID string `json:"accountId,omitempty" jsonschema:"narrow to one account id, from list_accounts"`

	GroupBy string `json:"groupBy,omitempty" jsonschema:"\"billing_cycle\" to frame the response around one account's statement periods; omit for calendar months"`
	Cycles  int    `json:"cycles,omitempty" jsonschema:"how many billing cycles to span when groupBy is billing_cycle, default 12, max 60"`
}

// moneyFlowArgs adds the per-stage node cap to the window.
type moneyFlowArgs struct {
	DateFrom  string `json:"dateFrom,omitempty" jsonschema:"inclusive start date, YYYY-MM-DD"`
	DateTo    string `json:"dateTo,omitempty" jsonschema:"inclusive end date, YYYY-MM-DD"`
	AccountID string `json:"accountId,omitempty" jsonschema:"narrow the graph to one account id"`
	Limit     int    `json:"limit,omitempty" jsonschema:"cap on the income, category and payee stages, default 12, max 30; the remainder of each stage collapses into one Other node"`
}

func dashboardTools() []Tool {
	return []Tool{
		{
			Name:  "get_dashboard_summary",
			Title: "Get the dashboard summary",
			Description: "The app's dashboard aggregate: account and transaction counts, income and expense totals, per-category spend " +
				"and income (top 15 each), a trend series and the most recent transactions. With groupBy=billing_cycle the whole " +
				"response is framed around one account's statement periods and accountId is required. Prefer this to summing a " +
				"transaction page.",
			Route:   readonly.Route{Method: http.MethodGet, Path: "/dashboard/summary"},
			install: installDashboardSummary,
		},
		{
			Name:  "get_money_flow",
			Title: "Get the money-flow graph",
			Description: "The Sankey graph behind the Money Flow page: income sources to accounts to categories to payees, with " +
				"cross-account transfer/refund/cashback/bill-payment links as real account-to-account edges (already cycle-free) and a " +
				"per-link-type rollup that is not drawn. Every node carries the total flowing through it.",
			Route:   readonly.Route{Method: http.MethodGet, Path: "/dashboard/money-flow"},
			install: installMoneyFlow,
		},
		{
			Name:  "get_money_flow_timeline",
			Title: "Get the money-flow timeline",
			Description: "One period of income, expense and net per calendar month, or per billing cycle when groupBy=billing_cycle " +
				"(which needs accountId). Each period carries its inclusive start and end dates, so a period can be handed straight " +
				"back to get_money_flow as dateFrom/dateTo.",
			Route:   readonly.Route{Method: http.MethodGet, Path: "/dashboard/money-flow/timeline"},
			install: installMoneyFlowTimeline,
		},
		{
			Name:  "get_cash_flow_calendar",
			Title: "Get the cash-flow calendar",
			Description: "Daily income, expense and net for every day in the window that has transactions, with window totals. " +
				"Selecting a single account also returns that account's billing-cycle boundaries and its synthetic summary markers " +
				"(month-end running balances, or per-cycle total outstanding), which are overlay data and excluded from the day totals.",
			Route:   readonly.Route{Method: http.MethodGet, Path: "/dashboard/cash-flow-calendar"},
			install: installCashFlowCalendar,
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
		})
	})
}
