package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/internal/money"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Per-stage node cap for the money-flow graph. The top N nodes by volume in the
// income, category, and payee stages are kept and the remainder collapse into a
// single "Other" node; account nodes are never capped (a user has few accounts
// and hiding one would misrepresent the flow).
const (
	defaultFlowNodeLimit = 12
	maxFlowNodeLimit     = 30
)

// flowIncomeRow is one grouped credit stream: a category of money entering an
// account. currency is the account's, because the money the row sums is money in
// that account's own ledger.
type flowIncomeRow struct {
	catID, catName, catColor string
	groupID, groupColor      string
	acctID, acctName         string
	acctColor                string
	currency                 string
	total                    money.Amount
}

// flowAcctCatRow is one grouped debit stream: an account paying into a category.
type flowAcctCatRow struct {
	acctID, acctName, acctColor string
	catID, catName, catColor    string
	groupID, groupColor         string
	currency                    string
	total                       money.Amount
}

// flowCatPayeeRow is one grouped debit stream: a category paying a payee.
type flowCatPayeeRow struct {
	catID, catName, catColor string
	groupID, groupColor      string
	payeeID, payeeName       string
	currency                 string
	total                    money.Amount
}

// flowLinkRow is a per-type rollup of the user's transaction links. One type can
// yield a row per currency, because the endpoints of a link need not agree, so
// the caller folds the rows of a type together.
type flowLinkRow struct {
	typ      string
	currency string
	count    int
	total    money.Amount
}

// flowAcctLinkRow is one raw link whose endpoints are in different accounts. It
// captures both endpoints' transaction types so the money direction (debit
// account -> credit account) can be derived regardless of how the link was
// stored. currency is the currency of amount — the account the amount was taken
// from — not necessarily the currency of the account the money flows out of.
type flowAcctLinkRow struct {
	fromType, toType                        string
	fromAcctID, fromAcctName, fromAcctColor string
	toAcctID, toAcctName, toAcctColor       string
	currency                                string
	amount                                  money.Amount
}

// flowAccountEdge is one directed account-to-account flow after netting and
// cycle-breaking, carrying the endpoint display metadata for the graph.
type flowAccountEdge struct {
	srcID, srcName, srcColor string
	dstID, dstName, dstColor string
	value                    models.CurrencyAmounts
}

// flowQueryer is the transactional read surface the flow queries use. *pgx.Tx
// satisfies it; keeping it narrow lets the query helpers stay testable.
type flowQueryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// GetMoneyFlow aggregates the user's transactions into a left-to-right Sankey
// graph (money sources → accounts → spending categories → payees) over an
// optional date range, account and currency filter. Cross-account links
// (transfers, refunds, cashbacks, bill payments) are drawn as account-to-account
// edges after netting reciprocal pairs and dropping DFS back edges, so the graph
// stays acyclic; the same links are still rolled up per type in LinkSummary.
//
// Every amount is per-currency. With no account filter the window can span an INR
// account and a USD one, and neither a node total, an edge width nor the
// headline figures can be a single number, so each carries one amount per
// currency alongside the scope that produced it.
//
// All reads run in a single read-only, repeatable-read transaction so the
// stages reflect one consistent snapshot.
func (srv *Server) GetMoneyFlow(c *gin.Context) {
	ctx := c
	userID := auth.GetUserID(c)
	dateFrom := c.Query("dateFrom")
	dateTo := c.Query("dateTo")
	accountID := c.Query("accountId")

	// The date bounds are compared against a date column and the account id
	// against a uuid column, so reject malformed filters up front instead of
	// letting them surface as 500s.
	dateFrom, ok := parseQueryDate(c, "dateFrom", dateFrom)
	if !ok {
		return
	}
	dateTo, ok = parseQueryDate(c, "dateTo", dateTo)
	if !ok {
		return
	}
	if accountID != "" {
		if _, err := uuid.Parse(accountID); err != nil {
			validation.RespondError(c, "invalid accountId", http.StatusBadRequest)
			return
		}
	}
	// The currency is parsed once here and passed down, so the scope query, the
	// stage queries and the link queries all narrow on the same normalised code.
	currency, ok := parseCurrency(c)
	if !ok {
		return
	}

	limit := defaultFlowNodeLimit
	if raw := c.Query("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			if n > maxFlowNodeLimit {
				n = maxFlowNodeLimit
			}
			limit = n
		}
	}

	tx, err := srv.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		slog.Error("GetMoneyFlow (begin)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(ctx)

	// The scope query carries the headline totals and the accounts behind them.
	// It reads accounts rather than transactions, so an account quiet in the
	// window still appears and explains the currency it holds; the stage queries
	// below are transaction-driven and cannot.
	scope, err := srv.currencyScope(ctx, tx, userID, scopeOptions{
		DateFrom:  dateFrom,
		DateTo:    dateTo,
		AccountID: accountID,
		Currency:  currency,
	})
	if err != nil {
		slog.Error("GetMoneyFlow (currency scope)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	incomeRows, err := queryIncomeFlows(ctx, tx, userID, dateFrom, dateTo, accountID, currency)
	if err != nil {
		slog.Error("GetMoneyFlow (income flows)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	acctCatRows, err := queryAccountCategoryFlows(ctx, tx, userID, dateFrom, dateTo, accountID, currency)
	if err != nil {
		slog.Error("GetMoneyFlow (account-category flows)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	catPayeeRows, err := queryCategoryPayeeFlows(ctx, tx, userID, dateFrom, dateTo, accountID, currency)
	if err != nil {
		slog.Error("GetMoneyFlow (category-payee flows)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	linkRows, err := queryMoneyFlowLinks(ctx, tx, userID, dateFrom, dateTo, accountID, currency)
	if err != nil {
		slog.Error("GetMoneyFlow (link summary)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	acctLinkRows, err := queryAccountLinkFlows(ctx, tx, userID, dateFrom, dateTo, accountID, currency)
	if err != nil {
		slog.Error("GetMoneyFlow (account links)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	graph := buildMoneyFlowGraph(incomeRows, acctCatRows, catPayeeRows, linkRows, acctLinkRows, limit)
	// The totals come from the scope rather than from summing the stage rows, so
	// the three headline figures and the nodes beside them describe the same
	// accounts. TotalNet is the server's per-currency difference: income minus
	// expense is only defined inside one currency, and only the server knows
	// which currencies are in scope.
	graph.TotalIncome = scope.Income
	graph.TotalExpense = scope.Expense
	graph.TotalNet = scope.Net()
	graph.CurrencyScope = scope.Scope

	if err := tx.Commit(ctx); err != nil {
		slog.Error("GetMoneyFlow (commit)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, graph)
}

// queryIncomeFlows groups every credit in the window by (category, account):
// the source categories flowing into each account. The account's currency is
// projected and grouped with them, so the same category earning into two
// currencies arrives as two rows and never becomes one total.
func queryIncomeFlows(ctx context.Context, db flowQueryer, userID uuid.UUID, dateFrom, dateTo, accountID, currency string) ([]flowIncomeRow, error) {
	cond, args, _ := flowFilter("t", "a", 2, dateFrom, dateTo, accountID, currency)
	filter := ""
	if cond != "" {
		filter = " AND " + cond
	}
	rows, err := db.Query(ctx, `
		SELECT COALESCE(c.id::text, ''), COALESCE(c.name, 'Uncategorized'), COALESCE(c.color, ''),
			   COALESCE(cg.id, ''), COALESCE(cg.color, ''),
			   a.id::text, a.name, a.color,
			   `+flowCurrency("a.currency")+` AS currency,
			   COALESCE(SUM(t.amount), 0)
		FROM transactions t
		JOIN accounts a ON t.account_id = a.id
		LEFT JOIN categories c ON t.category_id = c.id
		LEFT JOIN category_groups cg ON c.group_id = cg.id
		WHERE t.user_id = $1 AND t.type = 'credit'`+filter+`
		GROUP BY c.id, c.name, c.color, cg.id, cg.color, a.id, a.name, a.color, `+flowCurrency("a.currency"),
		append([]any{userID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []flowIncomeRow
	for rows.Next() {
		var r flowIncomeRow
		if err := rows.Scan(&r.catID, &r.catName, &r.catColor, &r.groupID, &r.groupColor,
			&r.acctID, &r.acctName, &r.acctColor, &r.currency, &r.total); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// queryAccountCategoryFlows groups every debit in the window by (account,
// category): where each account's money goes. The account's currency rides along
// for the same reason as in queryIncomeFlows: one category spent from an INR
// account and a USD one is two flows, not one.
func queryAccountCategoryFlows(ctx context.Context, db flowQueryer, userID uuid.UUID, dateFrom, dateTo, accountID, currency string) ([]flowAcctCatRow, error) {
	cond, args, _ := flowFilter("t", "a", 2, dateFrom, dateTo, accountID, currency)
	filter := ""
	if cond != "" {
		filter = " AND " + cond
	}
	rows, err := db.Query(ctx, `
		SELECT a.id::text, a.name, a.color,
			   COALESCE(c.id::text, ''), COALESCE(c.name, 'Uncategorized'), COALESCE(c.color, ''),
			   COALESCE(cg.id, ''), COALESCE(cg.color, ''),
			   `+flowCurrency("a.currency")+` AS currency,
			   COALESCE(SUM(t.amount), 0)
		FROM transactions t
		JOIN accounts a ON t.account_id = a.id
		LEFT JOIN categories c ON t.category_id = c.id
		LEFT JOIN category_groups cg ON c.group_id = cg.id
		WHERE t.user_id = $1 AND t.type = 'debit'`+filter+`
		GROUP BY a.id, a.name, a.color, c.id, c.name, c.color, cg.id, cg.color, `+flowCurrency("a.currency"),
		append([]any{userID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []flowAcctCatRow
	for rows.Next() {
		var r flowAcctCatRow
		if err := rows.Scan(&r.acctID, &r.acctName, &r.acctColor,
			&r.catID, &r.catName, &r.catColor, &r.groupID, &r.groupColor,
			&r.currency, &r.total); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// queryCategoryPayeeFlows groups every debit in the window by (category,
// payee): where each category's money ends up. This is also the authoritative
// source of category-stage totals (it covers every debit exactly once).
//
// The account's currency is in the grouping, not merely in the select. Grouping by
// category and payee alone is what let debits in two currencies be merged into a
// single row by the database, before any map existed to keep them apart: one
// category paying one payee from a rupee account and a dollar account came back
// as one amount that already no longer meant anything.
func queryCategoryPayeeFlows(ctx context.Context, db flowQueryer, userID uuid.UUID, dateFrom, dateTo, accountID, currency string) ([]flowCatPayeeRow, error) {
	cond, args, _ := flowFilter("t", "a", 2, dateFrom, dateTo, accountID, currency)
	filter := ""
	if cond != "" {
		filter = " AND " + cond
	}
	rows, err := db.Query(ctx, `
		SELECT COALESCE(c.id::text, ''), COALESCE(c.name, 'Uncategorized'), COALESCE(c.color, ''),
			   COALESCE(cg.id, ''), COALESCE(cg.color, ''),
			   COALESCE(p.id::text, ''), COALESCE(p.name, 'No payee'),
			   `+flowCurrency("a.currency")+` AS currency,
			   COALESCE(SUM(t.amount), 0)
		FROM transactions t
		JOIN accounts a ON t.account_id = a.id
		LEFT JOIN categories c ON t.category_id = c.id
		LEFT JOIN category_groups cg ON c.group_id = cg.id
		LEFT JOIN payees p ON t.payee_id = p.id
		WHERE t.user_id = $1 AND t.type = 'debit'`+filter+`
		GROUP BY c.id, c.name, c.color, cg.id, cg.color, p.id, p.name, `+flowCurrency("a.currency"),
		append([]any{userID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []flowCatPayeeRow
	for rows.Next() {
		var r flowCatPayeeRow
		if err := rows.Scan(&r.catID, &r.catName, &r.catColor, &r.groupID, &r.groupColor,
			&r.payeeID, &r.payeeName, &r.currency, &r.total); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// queryMoneyFlowLinks rolls the user's links up by type, valued at the debit
// side of each pair (or the credit when neither side is a debit). A link is
// included when either endpoint falls inside the window/account filter, so an
// account filter still surfaces that account's transfers to other accounts.
//
// The rollup is grouped by the currency of the value as well as by type, because
// a link has two accounts: summing a type's amounts across them is exactly the
// cross-currency total this endpoint is changing to refuse.
func queryMoneyFlowLinks(ctx context.Context, db flowQueryer, userID uuid.UUID, dateFrom, dateTo, accountID, currency string) ([]flowLinkRow, error) {
	fromCond, fromArgs, next := flowFilter("ft", "fa", 2, dateFrom, dateTo, accountID, "")
	toCond, toArgs, next := flowFilter("tt", "ta", next, dateFrom, dateTo, accountID, "")
	either := combineFlowConds(fromCond, toCond)

	args := append([]any{userID}, fromArgs...)
	args = append(args, toArgs...)

	// The currency predicate joins the endpoint predicates rather than going
	// inside either of them, and it filters on the value's currency: admitting a
	// link because *one* endpoint is in the requested currency would report its
	// amount in whichever currency the amount happens to be denominated in, which
	// may be the one the caller filtered out.
	if currency != "" {
		either += " AND " + flowCurrencyPredicate(linkCurrencyColumn, next)
		args = append(args, currency)
	}

	rows, err := db.Query(ctx, `
		SELECT l.type, COUNT(*),
			   `+flowCurrency(linkCurrencyColumn)+` AS currency,
			   COALESCE(SUM(CASE WHEN ft.type = 'debit' THEN ft.amount
			                     WHEN tt.type = 'debit' THEN tt.amount
			                     ELSE tt.amount END), 0)
		FROM links l
		JOIN transactions ft ON l.from_txn_id = ft.id AND ft.user_id = l.user_id
		JOIN accounts fa ON ft.account_id = fa.id
		JOIN transactions tt ON l.to_txn_id = tt.id AND tt.user_id = l.user_id
		JOIN accounts ta ON tt.account_id = ta.id
		WHERE l.user_id = $1`+either+`
		GROUP BY l.type, `+flowCurrency(linkCurrencyColumn)+`
		ORDER BY l.type`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []flowLinkRow
	for rows.Next() {
		var r flowLinkRow
		if err := rows.Scan(&r.typ, &r.count, &r.currency, &r.total); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// queryAccountLinkFlows returns each link whose endpoints sit in different
// accounts, carrying both endpoints' transaction types so the money direction
// can be derived. The same either-endpoint window predicate as the link summary
// applies: a transfer is in scope when either side falls inside the window.
func queryAccountLinkFlows(ctx context.Context, db flowQueryer, userID uuid.UUID, dateFrom, dateTo, accountID, currency string) ([]flowAcctLinkRow, error) {
	fromCond, fromArgs, next := flowFilter("ft", "fa", 2, dateFrom, dateTo, accountID, "")
	toCond, toArgs, next := flowFilter("tt", "ta", next, dateFrom, dateTo, accountID, "")
	either := combineFlowConds(fromCond, toCond)

	args := append([]any{userID}, fromArgs...)
	args = append(args, toArgs...)

	if currency != "" {
		either += " AND " + flowCurrencyPredicate(linkCurrencyColumn, next)
		args = append(args, currency)
	}

	rows, err := db.Query(ctx, `
		SELECT ft.type, tt.type,
			   fa.id::text, fa.name, fa.color,
			   ta.id::text, ta.name, ta.color,
			   `+flowCurrency(linkCurrencyColumn)+` AS currency,
			   CASE WHEN ft.type = 'debit' THEN ft.amount ELSE tt.amount END
		FROM links l
		JOIN transactions ft ON l.from_txn_id = ft.id AND ft.user_id = l.user_id
		JOIN accounts fa ON ft.account_id = fa.id
		JOIN transactions tt ON l.to_txn_id = tt.id AND tt.user_id = l.user_id
		JOIN accounts ta ON tt.account_id = ta.id
		WHERE l.user_id = $1`+either+`
		  AND fa.id <> ta.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []flowAcctLinkRow
	for rows.Next() {
		var r flowAcctLinkRow
		if err := rows.Scan(&r.fromType, &r.toType,
			&r.fromAcctID, &r.fromAcctName, &r.fromAcctColor,
			&r.toAcctID, &r.toAcctName, &r.toAcctColor,
			&r.currency, &r.amount); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// combineFlowConds joins the two endpoint predicate groups for a link query,
// wrapped in parentheses and OR-ed when both are present. It returns the
// fragment including its leading " AND " (empty when neither endpoint is
// filtered), matching the call sites' splicing convention.
func combineFlowConds(fromCond, toCond string) string {
	switch {
	case fromCond == "" && toCond == "":
		return ""
	case fromCond == "":
		return " AND (" + toCond + ")"
	case toCond == "":
		return " AND (" + fromCond + ")"
	default:
		return " AND ((" + fromCond + ") OR (" + toCond + "))"
	}
}

// flowCycleEdge is one directed account leg of a circular flow. value is the
// leg's gross flow — what circulated — and discarded is the part of it that ended
// up in no edge, filled in once the whole graph is known. gross and drawn differ
// for two separate reasons, and both are reported: netting cancels a currency
// against its own reverse, and the cycle break drops a leg wholesale.
type flowCycleEdge struct {
	srcID, dstID string
	value        models.CurrencyAmounts
	discarded    models.CurrencyAmounts
}

// flowCycle is one circular money flow between accounts. kind is "reciprocal"
// for a pair that flows both ways (netted into a single Sankey edge) or "cycle"
// for a longer loop broken by dropping its back edge. nodes lists the accounts
// in flow order, each leg running from nodes[i] to nodes[(i+1)%len(nodes)].
type flowCycle struct {
	kind  string
	nodes []string
	legs  []flowCycleEdge
}

// flowAccountEnd is the display identity of one endpoint of an account flow.
type flowAccountEnd struct{ id, name, color string }

// flowLinkEnds resolves the directed account pair one raw cross-account link
// contributes: money flows from the debit side to the credit side, or in the
// stored orientation when both sides are the same kind. ok is false for a link
// whose endpoints sit on the same account — there is no account-to-account flow
// to draw (a same-account refund or cashback, for instance).
func flowLinkEnds(r flowAcctLinkRow) (key [2]string, src, dst flowAccountEnd, ok bool) {
	from := flowAccountEnd{r.fromAcctID, r.fromAcctName, r.fromAcctColor}
	to := flowAccountEnd{r.toAcctID, r.toAcctName, r.toAcctColor}
	src, dst = from, to
	if r.fromType == "credit" && r.toType == "debit" {
		src, dst = to, from
	}
	if src.id == dst.id {
		return [2]string{}, flowAccountEnd{}, flowAccountEnd{}, false
	}
	return [2]string{src.id, dst.id}, src, dst, true
}

// analyzeAccountFlows turns raw cross-account links into directed account flows
// (debit account -> credit account), then nets reciprocal pairs and drops the
// remaining back edges so the returned edges form a DAG the Sankey can render.
// The cycles this discards are returned alongside them, each leg carrying both
// what circulated and what the graph did not keep, so both the graph
// (`MoneyFlowGraph.SuppressedCycles`) and the circular-money report
// (GetLinkCycles) can say what had to be hidden rather than only that something
// was. The processing order is deterministic (sorted ids), so the same input
// always yields the same edges and the same cycles.
//
// Which edges are drawn is decided by identity - the node pair, then the DFS
// stack - not by which amount is larger, so making every value per-currency does
// not re-order or re-choose among the graph's shapes. The one place amounts are
// compared is the netting below, and that comparison now happens within one
// currency: a pair whose two sides are in different currencies cannot be reduced
// to one edge, so it stays a two-way flow and the DFS breaks it like any other
// cycle. The reciprocal cycle carries the gross, per-currency legs either way.
func analyzeAccountFlows(rows []flowAcctLinkRow) ([]flowAccountEdge, []flowCycle) {
	agg := map[[2]string]*flowAccountEdge{}
	// gross is agg before netting, keyed the same way, and is what a cycle leg
	// reports: what circulated between the two accounts, not what survived. The
	// graph is required to stay acyclic and a cycle is required to be reported
	// with what it removed, and a report of the post-netting figure would answer
	// neither.
	gross := map[[2]string]models.CurrencyAmounts{}
	for _, r := range rows {
		key, src, dst, ok := flowLinkEnds(r)
		if !ok {
			continue
		}
		if e, ok := agg[key]; ok {
			e.value = e.value.Add(r.currency, r.amount)
			gross[key] = gross[key].Add(r.currency, r.amount)
			continue
		}
		agg[key] = &flowAccountEdge{
			srcID: src.id, srcName: src.name, srcColor: src.color,
			dstID: dst.id, dstName: dst.name, dstColor: dst.color,
			value: models.NewCurrencyAmounts().Add(r.currency, r.amount),
		}
		gross[key] = models.NewCurrencyAmounts().Add(r.currency, r.amount)
	}
	// Net reciprocal pairs into a single edge in the dominant direction. This
	// removes the common A->B / B->A two-cycles outright; each one is reported
	// as a "reciprocal" cycle before it is netted away.
	var cycles []flowCycle
	keys := make([][2]string, 0, len(agg))
	for key := range agg {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	seen := map[[2]string]bool{}
	for _, key := range keys {
		edge, ok := agg[key]
		if !ok || seen[key] {
			continue
		}
		seen[key] = true
		rev := [2]string{key[1], key[0]}
		revEdge, ok := agg[rev]
		if !ok {
			continue
		}
		seen[rev] = true
		// The cycle records the gross legs, before netting, so the report can
		// show what circulated rather than what survived. It is recorded even when
		// netting does resolve the pair, and equally when it cannot: a pair whose
		// two sides are in different currencies stays a two-way flow, and the DFS
		// below hides the one leg that would close the cycle.
		cycles = append(cycles, flowCycle{
			kind:  "reciprocal",
			nodes: []string{key[0], key[1]},
			legs: []flowCycleEdge{
				{srcID: key[0], dstID: key[1], value: gross[key]},
				{srcID: key[1], dstID: key[0], value: gross[rev]},
			},
		})
		edge.value, revEdge.value = netFlowAmounts(edge.value, revEdge.value)
		if len(edge.value) == 0 {
			delete(agg, key)
		}
		if len(revEdge.value) == 0 {
			delete(agg, rev)
		}
	}

	// Drop back edges to break any longer cycles: a depth-first walk keeps
	// every edge except those pointing at a node still on the recursion stack.
	// Each dropped back edge closes a cycle, so the recursion stack at that
	// moment spells out the loop being hidden.
	adj := map[string][]string{}
	for key := range agg {
		adj[key[0]] = append(adj[key[0]], key[1])
	}
	for id := range adj {
		sort.Strings(adj[id])
	}
	state := map[string]int{} // 0 unvisited, 1 on stack, 2 done
	stack := []string{}
	kept := []flowAccountEdge{}
	// dropped records the edges the break removed, so each cycle leg can report
	// what it cost once the whole graph is known.
	dropped := map[[2]string]bool{}
	var visit func(string)
	visit = func(u string) {
		state[u] = 1
		stack = append(stack, u)
		for _, v := range adj[u] {
			key := [2]string{u, v}
			edge, ok := agg[key]
			if !ok {
				continue
			}
			switch state[v] {
			case 1:
				// Back edge: dropping it breaks the cycle. The stack holds the
				// cycle's participants in flow order.
				dropped[key] = true
				if cycle, ok := flowStackCycle(gross, stack, v, gross[key]); ok {
					cycles = append(cycles, cycle)
				}
			case 0:
				kept = append(kept, *edge)
				visit(v)
			default:
				kept = append(kept, *edge)
			}
		}
		stack = stack[:len(stack)-1]
		state[u] = 2
	}
	roots := make([]string, 0, len(adj))
	for id := range adj {
		roots = append(roots, id)
	}
	sort.Strings(roots)
	for _, id := range roots {
		if state[id] == 0 {
			visit(id)
		}
	}

	// What each leg lost, now that every edge's fate is settled: its gross minus
	// what the graph actually carries for that pair, currency by currency. A leg
	// the break dropped lost all of it, and netting's cancellation is a loss from
	// both directions of a pair — the same 40 rupees, reported against each leg it
	// was taken from, because that is what each leg's flow was reduced by.
	for i := range cycles {
		for j := range cycles[i].legs {
			leg := &cycles[i].legs[j]
			var shown models.CurrencyAmounts
			if e, ok := agg[[2]string{leg.srcID, leg.dstID}]; ok && !dropped[[2]string{leg.srcID, leg.dstID}] {
				shown = e.value
			}
			leg.discarded = models.NewCurrencyAmounts()
			for code, amount := range leg.value {
				if rest := amount - shown[code]; rest != 0 {
					leg.discarded[code] = rest
				}
			}
		}
	}

	sort.Slice(kept, func(i, j int) bool {
		if kept[i].srcID != kept[j].srcID {
			return kept[i].srcID < kept[j].srcID
		}
		return kept[i].dstID < kept[j].dstID
	})

	return kept, dedupeAndSortCycles(cycles)
}

// netFlowAmounts cancels two opposing amounts currency by currency and returns
// what survives in each direction. Cancellation is only defined inside one
// currency - a rupee cannot cancel a dollar - so each code is resolved on its own
// and a reciprocal pair whose two sides are in different currencies survives in
// both directions instead of being collapsed into whichever side happened to hold
// the larger number. A side that nets away entirely comes back empty, which is
// how the caller knows the edge no longer exists. Neither input is modified, and
// the caller keeps the pre-netting figures separately, so what the netting
// removed is still available to report.
func netFlowAmounts(fwd, rev models.CurrencyAmounts) (models.CurrencyAmounts, models.CurrencyAmounts) {
	outFwd, outRev := models.NewCurrencyAmounts(), models.NewCurrencyAmounts()
	for code, amount := range fwd {
		if net := amount - rev[code]; net > 0 {
			outFwd[code] = net
		}
	}
	for code, amount := range rev {
		if net := amount - fwd[code]; net > 0 {
			outRev[code] = net
		}
	}
	return outFwd, outRev
}

// flowStackCycle builds the cycle closed by a back edge from the current DFS
// stack: v is the already-on-stack node the edge points back at, so the loop is
// stack[indexOf(v):] plus the closing leg. Legs carry the gross, pre-netting flow
// of each consecutive pair — the same figure a reciprocal pair reports, so the two
// kinds of cycle are read the same way.
func flowStackCycle(gross map[[2]string]models.CurrencyAmounts, stack []string, v string, closing models.CurrencyAmounts) (flowCycle, bool) {
	start := -1
	for i, id := range stack {
		if id == v {
			start = i
			break
		}
	}
	if start < 0 {
		return flowCycle{}, false
	}
	nodes := append([]string{}, stack[start:]...)
	if len(nodes) < 2 {
		return flowCycle{}, false
	}
	legs := make([]flowCycleEdge, 0, len(nodes))
	for i := range nodes {
		if i == len(nodes)-1 {
			legs = append(legs, flowCycleEdge{srcID: nodes[i], dstID: nodes[0], value: closing})
			continue
		}
		value, ok := gross[[2]string{nodes[i], nodes[i+1]}]
		if !ok {
			return flowCycle{}, false
		}
		legs = append(legs, flowCycleEdge{srcID: nodes[i], dstID: nodes[i+1], value: value})
	}
	return flowCycle{kind: "cycle", nodes: nodes, legs: legs}, true
}

// dedupeAndSortCycles drops duplicate loops (the same accounts can be closed by
// more than one back edge) and orders the result deterministically: reciprocal
// two-cycles first, then by participant id.
func dedupeAndSortCycles(cycles []flowCycle) []flowCycle {
	unique := make([]flowCycle, 0, len(cycles))
	seen := map[string]bool{}
	for _, c := range cycles {
		ids := append([]string{}, c.nodes...)
		sort.Strings(ids)
		key := strings.Join(ids, "|")
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, c)
	}
	sort.SliceStable(unique, func(i, j int) bool {
		if (unique[i].kind == "reciprocal") != (unique[j].kind == "reciprocal") {
			return unique[i].kind == "reciprocal"
		}
		return strings.Join(unique[i].nodes, "|") < strings.Join(unique[j].nodes, "|")
	})
	return unique
}

// flowCurrency is the one expression this file projects, groups by and filters
// on to read an account's currency. accounts.currency is nullable and a restored
// bundle can hold the empty string, so a site that dropped the NULLIF would
// disagree with the others and drop a default-currency account from an explicit
// ?currency=INR report while the rest of the same response still called it INR.
// See the scopeSQL comment in currency.go for the full argument.
//
// It takes the column rather than the alias because the queries do not agree on
// an alias: the four transaction-driven ones join `accounts a`, while the two
// link queries reach the currency through `fa`/`ta`. Everything else about the
// expression - the NULLIF, the default, the parenthesisation - comes from here,
// so a future edit cannot make one site's spelling differ from another's.
func flowCurrency(column string) string {
	return fmt.Sprintf("COALESCE(NULLIF(%s, ''), '%s')", column, defaultCurrency)
}

// flowCurrencyPredicate is flowCurrency bound to a placeholder, for the
// ?currency= filter. The expression is the one above for the reason that comment
// gives: a predicate that compared the raw column would exclude every account
// whose currency is merely unset.
func flowCurrencyPredicate(column string, placeholder int) string {
	return fmt.Sprintf("%s = $%d", flowCurrency(column), placeholder)
}

// linkCurrencyColumn names the account whose currency a link's value is
// denominated in. The value is one of the two transactions' amounts, picked by
// the same CASE that picks the amount itself, and a transaction's amount is
// always in its own account's currency — so the currency of a link is the
// currency of the account the CASE picked, and a link between two accounts in
// different currencies reports one currency rather than a converted sum. The
// inconsistency stays visible in the scope instead of being added away.
const linkCurrencyColumn = "CASE WHEN ft.type = 'debit' THEN fa.currency ELSE ta.currency END"

// flowFilter renders the shared date/account/currency predicates for one
// table-alias pair, binding values into args and returning the rendered
// predicate (no leading conjunction) plus the arguments and the next free
// parameter index. currency is empty when no code was requested, which is also
// what the two link queries pass: they narrow on the currency of the value
// instead, which is not the same thing as either endpoint's account currency.
func flowFilter(dateAlias, accountAlias string, start int, dateFrom, dateTo, accountID, currency string) (string, []any, int) {
	var parts []string
	var args []any
	n := start
	if dateFrom != "" {
		parts = append(parts, fmt.Sprintf("%s.date >= $%d", dateAlias, n))
		args = append(args, dateFrom)
		n++
	}
	if dateTo != "" {
		parts = append(parts, fmt.Sprintf("%s.date <= $%d", dateAlias, n))
		args = append(args, dateTo)
		n++
	}
	if accountID != "" {
		parts = append(parts, fmt.Sprintf("%s.id = $%d", accountAlias, n))
		args = append(args, accountID)
		n++
	}
	if currency != "" {
		// The predicate belongs in the WHERE, which for a transaction-driven
		// query is where the inner join to accounts makes it equivalent to the
		// join's ON clause: a row that fails it has no account in scope at all.
		// The opposite requirement — never drop a quiet account — belongs to the
		// account-driven scope query, and lives in currencyScope.
		parts = append(parts, flowCurrencyPredicate(accountAlias+".currency", n))
		args = append(args, currency)
		n++
	}
	return strings.Join(parts, " AND "), args, n
}

// Node id constructors. "uncategorized" and "none" are explicit nodes rather
// than omitted, so every transaction contributes to the graph.
func flowIncomeNodeID(catID string) string {
	if catID == "" {
		return "income:uncategorized"
	}
	return "income:" + catID
}

func flowAccountNodeID(id string) string { return "account:" + id }

func flowCategoryNodeID(catID string) string {
	if catID == "" {
		return "category:uncategorized"
	}
	return "category:" + catID
}

func flowPayeeNodeID(payeeID string) string {
	if payeeID == "" {
		return "payee:none"
	}
	return "payee:" + payeeID
}

// buildMoneyFlowGraph assembles the response: it aggregates per-stage nodes,
// caps the income/category/payee stages at limit (rolling the tail into an
// "Other" node), then aggregates the edges through the same rollup so a node
// and its edges stay consistent.
//
// Every total it produces is per-currency, because a node aggregates across
// accounts. The three headline totals are left empty here: they come from the
// currency-scope query, which reads accounts rather than transactions and so also
// covers the accounts with no activity in the window; the caller fills them in.
func buildMoneyFlowGraph(incomeRows []flowIncomeRow, acctCatRows []flowAcctCatRow, catPayeeRows []flowCatPayeeRow, linkRows []flowLinkRow, acctLinkRows []flowAcctLinkRow, limit int) models.MoneyFlowGraph {
	incomeNodes := map[string]*models.MoneyFlowNode{}
	acctNodes := map[string]*models.MoneyFlowNode{}
	catNodes := map[string]*models.MoneyFlowNode{}
	payeeNodes := map[string]*models.MoneyFlowNode{}
	acctIn := map[string]models.CurrencyAmounts{}
	acctOut := map[string]models.CurrencyAmounts{}

	addNode := func(m map[string]*models.MoneyFlowNode, kind, id, name, color, group, currency string, amount money.Amount) {
		if n, ok := m[id]; ok {
			n.Total = n.Total.Add(currency, amount)
			return
		}
		m[id] = &models.MoneyFlowNode{
			ID: id, Name: name, Kind: kind, Color: color, Group: group,
			Total: models.NewCurrencyAmounts().Add(currency, amount),
		}
	}
	addAccountFlow := func(m map[string]models.CurrencyAmounts, id, currency string, amount money.Amount) {
		cur, ok := m[id]
		if !ok {
			cur = models.NewCurrencyAmounts()
		}
		m[id] = cur.Add(currency, amount)
	}

	// Income category color is the category's own swatch; the group is carried
	// for context. Account node totals are set below from the larger of their
	// inflow and outflow, so they are added with a zero amount here.
	for _, r := range incomeRows {
		color := r.catColor
		if color == "" {
			color = r.groupColor
		}
		addNode(incomeNodes, "income", flowIncomeNodeID(r.catID), r.catName, color, r.groupID, r.currency, r.total)
		addNode(acctNodes, "account", flowAccountNodeID(r.acctID), r.acctName, r.acctColor, "", r.currency, 0)
		addAccountFlow(acctIn, flowAccountNodeID(r.acctID), r.currency, r.total)
	}

	for _, r := range acctCatRows {
		addNode(acctNodes, "account", flowAccountNodeID(r.acctID), r.acctName, r.acctColor, "", r.currency, 0)
		addAccountFlow(acctOut, flowAccountNodeID(r.acctID), r.currency, r.total)
	}

	// Category and payee totals both come from the category→payee grouping, so
	// each debit is counted once. Category nodes are colored by their base
	// group; the category's own swatch is the fallback.
	for _, r := range catPayeeRows {
		color := r.groupColor
		if color == "" {
			color = r.catColor
		}
		addNode(catNodes, "category", flowCategoryNodeID(r.catID), r.catName, color, r.groupID, r.currency, r.total)
		addNode(payeeNodes, "payee", flowPayeeNodeID(r.payeeID), r.payeeName, "", "", r.currency, r.total)
	}

	// Cross-account link flows (transfers, refunds, cashbacks, bill payments)
	// after netting and cycle-breaking so the account subgraph stays acyclic.
	// An endpoint may be an account with no other activity in the window, so
	// ensure its node exists before accumulating the link volume. An edge's value
	// carries the currency of the amount it was taken from, which is not
	// necessarily the currency of the account the money flows out of — a link
	// between two differently denominated accounts records that inconsistency
	// rather than converting it away.
	//
	// The cycles come back with the edges and are carried into the response: the
	// break removed money from the graph, and a node total that quietly lacks a
	// currency the scope names is the exact silence this change exists to end.
	acctEdges, cycles := analyzeAccountFlows(acctLinkRows)
	for _, e := range acctEdges {
		addNode(acctNodes, "account", flowAccountNodeID(e.srcID), e.srcName, e.srcColor, "", "", 0)
		addNode(acctNodes, "account", flowAccountNodeID(e.dstID), e.dstName, e.dstColor, "", "", 0)
		for code, amount := range e.value {
			addAccountFlow(acctOut, flowAccountNodeID(e.srcID), code, amount)
			addAccountFlow(acctIn, flowAccountNodeID(e.dstID), code, amount)
		}
	}

	// Account node volume is the larger of what flowed in and what flowed out,
	// so the node reads as the money that passed through it. The comparison is
	// per currency, because that is the only one that means anything.
	for id, n := range acctNodes {
		n.Total = maxFlowAmounts(acctIn[id], acctOut[id])
	}

	incomeKept := keepTopFlowNodes(incomeNodes, limit)
	catKept := keepTopFlowNodes(catNodes, limit)
	payeeKept := keepTopFlowNodes(payeeNodes, limit)

	var nodes []models.MoneyFlowNode
	nodes = append(nodes, orderedFlowNodes(incomeNodes, incomeKept, "income", "income:other", "Other income")...)
	nodes = append(nodes, orderedFlowNodes(acctNodes, nil, "account", "", "")...)
	nodes = append(nodes, orderedFlowNodes(catNodes, catKept, "category", "category:other", "Other categories")...)
	nodes = append(nodes, orderedFlowNodes(payeeNodes, payeeKept, "payee", "payee:other", "Other payees")...)

	type edge struct {
		source, target string
		value          models.CurrencyAmounts
	}
	edges := map[string]*edge{}
	addEdge := func(source, target, currency string, value money.Amount) {
		if value <= 0 {
			return
		}
		key := source + "\x00" + target
		e, ok := edges[key]
		if !ok {
			e = &edge{source: source, target: target, value: models.NewCurrencyAmounts()}
			edges[key] = e
		}
		e.value = e.value.Add(currency, value)
	}

	rollup := func(id string, kept map[string]bool, other string) string {
		if kept[id] {
			return id
		}
		return other
	}

	for _, r := range incomeRows {
		addEdge(
			rollup(flowIncomeNodeID(r.catID), incomeKept, "income:other"),
			flowAccountNodeID(r.acctID),
			r.currency,
			r.total,
		)
	}
	for _, r := range acctCatRows {
		addEdge(
			flowAccountNodeID(r.acctID),
			rollup(flowCategoryNodeID(r.catID), catKept, "category:other"),
			r.currency,
			r.total,
		)
	}
	for _, r := range catPayeeRows {
		addEdge(
			rollup(flowCategoryNodeID(r.catID), catKept, "category:other"),
			rollup(flowPayeeNodeID(r.payeeID), payeeKept, "payee:other"),
			r.currency,
			r.total,
		)
	}
	for _, e := range acctEdges {
		for code, amount := range e.value {
			addEdge(flowAccountNodeID(e.srcID), flowAccountNodeID(e.dstID), code, amount)
		}
	}

	links := make([]models.MoneyFlowEdge, 0, len(edges))
	for _, e := range edges {
		links = append(links, models.MoneyFlowEdge{Source: e.source, Target: e.target, Value: e.value})
	}
	sort.Slice(links, func(i, j int) bool {
		if links[i].Source != links[j].Source {
			return links[i].Source < links[j].Source
		}
		return links[i].Target < links[j].Target
	})

	// One entry per link type, however many currencies its links spanned: the
	// query returns a row per (type, currency), and a type whose links sit in two
	// currencies is one summary holding two amounts, not two summaries.
	byType := map[string]*models.MoneyFlowLinkSummary{}
	var typeOrder []string
	for _, r := range linkRows {
		s, ok := byType[r.typ]
		if !ok {
			s = &models.MoneyFlowLinkSummary{Type: r.typ, Total: models.NewCurrencyAmounts()}
			byType[r.typ] = s
			typeOrder = append(typeOrder, r.typ)
		}
		s.Count += r.count
		s.Total = s.Total.Add(r.currency, r.total)
	}
	summary := make([]models.MoneyFlowLinkSummary, 0, len(typeOrder))
	for _, typ := range typeOrder {
		summary = append(summary, *byType[typ])
	}

	if nodes == nil {
		nodes = []models.MoneyFlowNode{}
	}

	return models.MoneyFlowGraph{
		Nodes:            nodes,
		Links:            links,
		TotalIncome:      models.NewCurrencyAmounts(),
		TotalExpense:     models.NewCurrencyAmounts(),
		TotalNet:         models.NewCurrencyAmounts(),
		LinkSummary:      summary,
		SuppressedCycles: suppressedFlowCycles(cycles),
	}
}

// suppressedFlowCycles renders the cycles the graph could not draw. Accounts are
// left as ids, because CurrencyScope normally names the participants with their
// display metadata and a second copy here would be a second thing to keep right.
// "Normally" is a real exception rather than a hedge: a link's value currency is
// the currency of the account the amount came from, not of both its endpoints, so
// under ?currency= a suppressed cycle can name an account CurrencyScope does not
// list. The spec says so on the field, and this is where the ids come from.
// The list is empty rather than nil so "nothing was hidden" is a fact the response
// states, not one a client has to infer from an absent field.
func suppressedFlowCycles(cycles []flowCycle) []models.MoneyFlowSuppressedCycle {
	out := make([]models.MoneyFlowSuppressedCycle, 0, len(cycles))
	for _, c := range cycles {
		entry := models.MoneyFlowSuppressedCycle{
			Kind:     c.kind,
			Accounts: append([]string{}, c.nodes...),
			Legs:     make([]models.MoneyFlowSuppressedLeg, 0, len(c.legs)),
		}
		for _, leg := range c.legs {
			entry.Legs = append(entry.Legs, models.MoneyFlowSuppressedLeg{
				From: leg.srcID, To: leg.dstID,
				Gross:     leg.value,
				Discarded: leg.discarded,
			})
		}
		out = append(out, entry)
	}
	return out
}

// maxFlowAmounts takes the larger of two per-currency totals, one currency at a
// time. With a single currency this is the larger of the two as before; with two
// it is the larger of each currency's, and no amount is ever compared against an
// amount of a different currency. A currency absent from both is absent here, so
// the result is always non-nil and marshals as {} rather than null.
//
// The comparison below reads a missing key as zero rather than guarding for it as
// minCycleAmounts does, and the two forms are equivalent only for non-negative
// operands: a currency at -500 against an absent key would come back as 0 rather
// than as -500. That is safe here and not there, and the reason is the caller.
// Both operands are folds of transaction amounts, which are stored positive with
// their direction in `type`, and Add skips a zero contribution, so an absent key
// is a currency with no money in it and every key present is >= 0. Under those
// operands "larger of the two, absent reading as zero" and "larger of the two
// where the other exists" cannot differ. TestMaxFlowAmountsReadsAMissingKeyAsZero
// pins that, so the equivalence is a stated property rather than a coincidence -
// a caller folding a signed quantity in here would need the guarded form.
func maxFlowAmounts(flowIn, flowOut models.CurrencyAmounts) models.CurrencyAmounts {
	out := models.NewCurrencyAmounts()
	for code, amount := range flowIn {
		if other := flowOut[code]; other > amount {
			amount = other
		}
		out[code] = amount
	}
	for code, amount := range flowOut {
		if _, ok := flowIn[code]; !ok {
			out[code] = amount
		}
	}
	return out
}

// compareFlowTotals orders two per-currency totals without ever adding across
// currencies. It walks the union of the key sets, code by code in sorted order,
// with an absent key reading as zero, so the order is total, depends on no
// arbitrary choice of a reference currency, and is exactly the by-amount order it
// replaces when there is only one currency. It returns 0 for equal totals, which
// leaves the caller's id tiebreak — and so the stability of a capped stage —
// deciding the order between two nodes that hold the same money.
func compareFlowTotals(a, b models.CurrencyAmounts) int {
	seen := make(map[string]bool, len(a)+len(b))
	codes := make([]string, 0, len(a)+len(b))
	for code := range a {
		if !seen[code] {
			seen[code] = true
			codes = append(codes, code)
		}
	}
	for code := range b {
		if !seen[code] {
			seen[code] = true
			codes = append(codes, code)
		}
	}
	sort.Strings(codes)
	for _, code := range codes {
		if a[code] == b[code] {
			continue
		}
		if a[code] > b[code] {
			return -1
		}
		return 1
	}
	return 0
}

// keepTopFlowNodes returns the ids of the limit highest-volume nodes. A nil or
// negative limit keeps nothing; limit >= len keeps everything. "Highest" is
// compareFlowTotals' order over the per-currency totals.
func keepTopFlowNodes(m map[string]*models.MoneyFlowNode, limit int) map[string]bool {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if c := compareFlowTotals(m[ids[i]].Total, m[ids[j]].Total); c != 0 {
			return c < 0
		}
		return ids[i] < ids[j]
	})

	kept := make(map[string]bool, len(ids))
	for i, id := range ids {
		if i >= limit {
			break
		}
		kept[id] = true
	}
	return kept
}

// orderedFlowNodes renders one stage: its kept nodes sorted by volume
// (highest first) followed by an aggregated "Other" node when any node was
// rolled up. kept == nil means the stage is never capped.
func orderedFlowNodes(m map[string]*models.MoneyFlowNode, kept map[string]bool, kind, otherID, otherName string) []models.MoneyFlowNode {
	ids := make([]string, 0, len(m))
	for id := range m {
		if kept == nil || kept[id] {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		if c := compareFlowTotals(m[ids[i]].Total, m[ids[j]].Total); c != 0 {
			return c < 0
		}
		return ids[i] < ids[j]
	})

	out := make([]models.MoneyFlowNode, 0, len(ids)+1)
	for _, id := range ids {
		out = append(out, *m[id])
	}

	if otherID != "" && len(kept) < len(m) {
		// The rolled-up node holds what its members held, per currency: a tail of
		// three nodes in two currencies is two amounts, and neither of them is
		// the sum of anything.
		other := models.MoneyFlowNode{ID: otherID, Name: otherName, Kind: kind, Total: models.NewCurrencyAmounts()}
		for id, n := range m {
			if kept[id] {
				continue
			}
			for code, amount := range n.Total {
				other.Total = other.Total.Add(code, amount)
			}
			if other.Color == "" {
				other.Color = n.Color
			}
		}
		out = append(out, other)
	}
	return out
}
