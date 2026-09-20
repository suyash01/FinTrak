package api

import "context"

// Dashboard, money flow, and the cash-flow calendar (backend/handlers/
// dashboard.go, money_flow.go, money_flow_timeline.go,
// cash_flow_calendar.go). All four routes answer with a bare typed struct
// rather than a data envelope.

// WindowFilter is the optional window shared by every dashboard route: an
// inclusive date range and/or a single account. The zero value means "the
// server's default window".
type WindowFilter struct {
	// DateFrom and DateTo bound the window inclusively, as YYYY-MM-DD. Either
	// bound may be left empty.
	DateFrom string
	DateTo   string
	// AccountID narrows every section to one account.
	AccountID string
}

// apply writes the window parameters, skipping empty ones.
func (f WindowFilter) apply(r *request) *request {
	return r.setQuery("dateFrom", f.DateFrom).
		setQuery("dateTo", f.DateTo).
		setQuery("accountId", f.AccountID)
}

// DashboardFilter is the window for Summary plus the statement-period framing.
type DashboardFilter struct {
	WindowFilter
	// GroupBy is "billing_cycle" to frame the dashboard around statement
	// periods. Anything else (including "") is the calendar-month view.
	GroupBy string
	// Cycles is how many billing cycles to include; it is ignored unless
	// GroupBy is "billing_cycle" and the server defaults to 12, capped at 60.
	Cycles int
}

// Summary returns the dashboard aggregate: account and transaction counts,
// income/expense totals, per-category spend and income (top 15 each), a trend,
// and the 10 most recent transactions.
//
// With GroupBy "billing_cycle" the whole dashboard is framed around the
// statement periods of one account, so AccountID is then REQUIRED (the server
// answers 400 without it, or when that account has no billing day). In that
// mode BillingCycleTrend replaces MonthlyTrend — one entry per cycle — and
// CurrentCycle describes the in-progress period the stat cards and recent
// transactions come from; Cycles selects how many cycles the window spans
// (default 12, max 60). The date-range filters are ignored in that mode, so
// leave DateFrom/DateTo zero when grouping by cycle. Leave Cycles zero to let
// the server default apply.
func (c *Client) Summary(ctx context.Context, f DashboardFilter) (DashboardSummary, error) {
	r := f.apply(get("/dashboard/summary")).
		setQuery("groupBy", f.GroupBy).
		setQueryInt("cycles", f.Cycles)
	return do[DashboardSummary](ctx, c, r)
}

// MoneyFlowFilter is the window for MoneyFlow plus the node cap.
type MoneyFlowFilter struct {
	WindowFilter
	// Limit caps the income, category and payee stages independently; the
	// server defaults to 12 and caps at 30. Leave it zero for the default.
	Limit int
}

// MoneyFlow returns the Sankey graph behind the Money Flow page: income →
// accounts → categories → payees, plus the link-type rollup that is not drawn
// as edges.
//
// Limit caps each of the income, category and payee stages (default 12, max 30)
// with the remainder of that stage collapsed into a single "Other" node, so a
// caller can keep the graph readable without losing the totals; account nodes
// are never capped. The account-to-account edges are already cycle-free:
// reciprocal pairs are netted into one edge and back edges are dropped, so the
// graph is safe to lay out as a DAG. LinkSummary carries the per-link-type
// counts and totals for the same window and should be shown alongside the
// graph rather than as edges. Leave Limit zero to let the server default apply.
func (c *Client) MoneyFlow(ctx context.Context, f MoneyFlowFilter) (MoneyFlowGraph, error) {
	r := f.apply(get("/dashboard/money-flow")).setQueryInt("limit", f.Limit)
	return do[MoneyFlowGraph](ctx, c, r)
}

// TimelineFilter is the window for MoneyFlowTimeline plus the period framing.
type TimelineFilter struct {
	WindowFilter
	// GroupBy is "billing_cycle" for statement periods; empty or anything else
	// is calendar months (the server's default).
	GroupBy string
	// Cycles is how many billing cycles to include; it is ignored unless
	// GroupBy is "billing_cycle" and the server defaults to 12, capped at 60.
	Cycles int
}

// MoneyFlowTimeline returns one period of income/expense/net per calendar
// month, or per billing cycle when GroupBy is "billing_cycle".
//
// The billing-cycle view needs AccountID (an account with a billing day) and
// honours Cycles (default 12, max 60). Each period carries inclusive
// StartDate/EndDate bounds that can be fed straight back into MoneyFlow —
// through WindowFilter.DateFrom/DateTo — to scrub the graph to the period the
// user clicked. Leave GroupBy/Cycles zero for the monthly view.
func (c *Client) MoneyFlowTimeline(ctx context.Context, f TimelineFilter) (MoneyFlowTimeline, error) {
	r := f.apply(get("/dashboard/money-flow/timeline")).
		setQuery("groupBy", f.GroupBy).
		setQueryInt("cycles", f.Cycles)
	return do[MoneyFlowTimeline](ctx, c, r)
}

// CashFlowCalendar returns the GitHub-style daily heatmap data: one entry per
// day that HAS transactions, so days with none are omitted and the UI fills the
// gaps to draw a continuous calendar. Totals cover the whole window.
//
// When AccountID selects a single account, the response also carries that
// account's billing-cycle boundaries (Cycles) and the synthetic summary markers
// already shown in the transaction list: month-end running balances, or the
// per-cycle total outstanding for an account with a billing day. Those markers
// are overlay data and are not included in the day totals.
func (c *Client) CashFlowCalendar(ctx context.Context, f WindowFilter) (CashFlowCalendar, error) {
	return do[CashFlowCalendar](ctx, c, f.apply(get("/dashboard/cash-flow-calendar")))
}
