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
// account.
type flowIncomeRow struct {
	catID, catName, catColor string
	groupID, groupColor      string
	acctID, acctName         string
	acctColor                string
	total                    money.Amount
}

// flowAcctCatRow is one grouped debit stream: an account paying into a category.
type flowAcctCatRow struct {
	acctID, acctName, acctColor string
	catID, catName, catColor    string
	groupID, groupColor         string
	total                       money.Amount
}

// flowCatPayeeRow is one grouped debit stream: a category paying a payee.
type flowCatPayeeRow struct {
	catID, catName, catColor string
	groupID, groupColor      string
	payeeID, payeeName       string
	total                    money.Amount
}

// flowLinkRow is a per-type rollup of the user's transaction links.
type flowLinkRow struct {
	typ   string
	count int
	total money.Amount
}

// flowAcctLinkRow is one raw link whose endpoints are in different accounts. It
// captures both endpoints' transaction types so the money direction (debit
// account -> credit account) can be derived regardless of how the link was
// stored.
type flowAcctLinkRow struct {
	fromType, toType                        string
	fromAcctID, fromAcctName, fromAcctColor string
	toAcctID, toAcctName, toAcctColor       string
	amount                                  money.Amount
}

// flowAccountEdge is one directed account-to-account flow after netting and
// cycle-breaking, carrying the endpoint display metadata for the graph.
type flowAccountEdge struct {
	srcID, srcName, srcColor string
	dstID, dstName, dstColor string
	value                    money.Amount
}

// flowQueryer is the transactional read surface the flow queries use. *pgx.Tx
// satisfies it; keeping it narrow lets the query helpers stay testable.
type flowQueryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// GetMoneyFlow aggregates the user's transactions into a left-to-right Sankey
// graph (money sources → accounts → spending categories → payees) over an
// optional date range and account filter. Cross-account links (transfers,
// refunds, cashbacks, bill payments) are drawn as account-to-account edges
// after netting reciprocal pairs and dropping DFS back edges, so the graph
// stays acyclic; the same links are still rolled up per type in LinkSummary.
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

	incomeRows, err := queryIncomeFlows(ctx, tx, userID, dateFrom, dateTo, accountID)
	if err != nil {
		slog.Error("GetMoneyFlow (income flows)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	acctCatRows, err := queryAccountCategoryFlows(ctx, tx, userID, dateFrom, dateTo, accountID)
	if err != nil {
		slog.Error("GetMoneyFlow (account-category flows)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	catPayeeRows, err := queryCategoryPayeeFlows(ctx, tx, userID, dateFrom, dateTo, accountID)
	if err != nil {
		slog.Error("GetMoneyFlow (category-payee flows)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	linkRows, err := queryMoneyFlowLinks(ctx, tx, userID, dateFrom, dateTo, accountID)
	if err != nil {
		slog.Error("GetMoneyFlow (link summary)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	acctLinkRows, err := queryAccountLinkFlows(ctx, tx, userID, dateFrom, dateTo, accountID)
	if err != nil {
		slog.Error("GetMoneyFlow (account links)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	graph := buildMoneyFlowGraph(incomeRows, acctCatRows, catPayeeRows, linkRows, acctLinkRows, limit)

	if err := tx.Commit(ctx); err != nil {
		slog.Error("GetMoneyFlow (commit)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, graph)
}

// queryIncomeFlows groups every credit in the window by (category, account):
// the source categories flowing into each account.
func queryIncomeFlows(ctx context.Context, db flowQueryer, userID uuid.UUID, dateFrom, dateTo, accountID string) ([]flowIncomeRow, error) {
	cond, args, _ := flowFilter("t", "a", 2, dateFrom, dateTo, accountID)
	filter := ""
	if cond != "" {
		filter = " AND " + cond
	}
	rows, err := db.Query(ctx, `
		SELECT COALESCE(c.id::text, ''), COALESCE(c.name, 'Uncategorized'), COALESCE(c.color, ''),
			   COALESCE(cg.id, ''), COALESCE(cg.color, ''),
			   a.id::text, a.name, a.color,
			   COALESCE(SUM(t.amount), 0)
		FROM transactions t
		JOIN accounts a ON t.account_id = a.id
		LEFT JOIN categories c ON t.category_id = c.id
		LEFT JOIN category_groups cg ON c.group_id = cg.id
		WHERE t.user_id = $1 AND t.type = 'credit'`+filter+`
		GROUP BY c.id, c.name, c.color, cg.id, cg.color, a.id, a.name, a.color`,
		append([]any{userID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []flowIncomeRow
	for rows.Next() {
		var r flowIncomeRow
		if err := rows.Scan(&r.catID, &r.catName, &r.catColor, &r.groupID, &r.groupColor,
			&r.acctID, &r.acctName, &r.acctColor, &r.total); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// queryAccountCategoryFlows groups every debit in the window by (account,
// category): where each account's money goes.
func queryAccountCategoryFlows(ctx context.Context, db flowQueryer, userID uuid.UUID, dateFrom, dateTo, accountID string) ([]flowAcctCatRow, error) {
	cond, args, _ := flowFilter("t", "a", 2, dateFrom, dateTo, accountID)
	filter := ""
	if cond != "" {
		filter = " AND " + cond
	}
	rows, err := db.Query(ctx, `
		SELECT a.id::text, a.name, a.color,
			   COALESCE(c.id::text, ''), COALESCE(c.name, 'Uncategorized'), COALESCE(c.color, ''),
			   COALESCE(cg.id, ''), COALESCE(cg.color, ''),
			   COALESCE(SUM(t.amount), 0)
		FROM transactions t
		JOIN accounts a ON t.account_id = a.id
		LEFT JOIN categories c ON t.category_id = c.id
		LEFT JOIN category_groups cg ON c.group_id = cg.id
		WHERE t.user_id = $1 AND t.type = 'debit'`+filter+`
		GROUP BY a.id, a.name, a.color, c.id, c.name, c.color, cg.id, cg.color`,
		append([]any{userID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []flowAcctCatRow
	for rows.Next() {
		var r flowAcctCatRow
		if err := rows.Scan(&r.acctID, &r.acctName, &r.acctColor,
			&r.catID, &r.catName, &r.catColor, &r.groupID, &r.groupColor, &r.total); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// queryCategoryPayeeFlows groups every debit in the window by (category,
// payee): where each category's money ends up. This is also the authoritative
// source of category-stage totals (it covers every debit exactly once).
func queryCategoryPayeeFlows(ctx context.Context, db flowQueryer, userID uuid.UUID, dateFrom, dateTo, accountID string) ([]flowCatPayeeRow, error) {
	cond, args, _ := flowFilter("t", "a", 2, dateFrom, dateTo, accountID)
	filter := ""
	if cond != "" {
		filter = " AND " + cond
	}
	rows, err := db.Query(ctx, `
		SELECT COALESCE(c.id::text, ''), COALESCE(c.name, 'Uncategorized'), COALESCE(c.color, ''),
			   COALESCE(cg.id, ''), COALESCE(cg.color, ''),
			   COALESCE(p.id::text, ''), COALESCE(p.name, 'No payee'),
			   COALESCE(SUM(t.amount), 0)
		FROM transactions t
		JOIN accounts a ON t.account_id = a.id
		LEFT JOIN categories c ON t.category_id = c.id
		LEFT JOIN category_groups cg ON c.group_id = cg.id
		LEFT JOIN payees p ON t.payee_id = p.id
		WHERE t.user_id = $1 AND t.type = 'debit'`+filter+`
		GROUP BY c.id, c.name, c.color, cg.id, cg.color, p.id, p.name`,
		append([]any{userID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []flowCatPayeeRow
	for rows.Next() {
		var r flowCatPayeeRow
		if err := rows.Scan(&r.catID, &r.catName, &r.catColor, &r.groupID, &r.groupColor,
			&r.payeeID, &r.payeeName, &r.total); err != nil {
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
func queryMoneyFlowLinks(ctx context.Context, db flowQueryer, userID uuid.UUID, dateFrom, dateTo, accountID string) ([]flowLinkRow, error) {
	fromCond, fromArgs, next := flowFilter("ft", "fa", 2, dateFrom, dateTo, accountID)
	toCond, toArgs, _ := flowFilter("tt", "ta", next, dateFrom, dateTo, accountID)
	either := combineFlowConds(fromCond, toCond)

	args := append([]any{userID}, fromArgs...)
	args = append(args, toArgs...)

	rows, err := db.Query(ctx, `
		SELECT l.type, COUNT(*),
			   COALESCE(SUM(CASE WHEN ft.type = 'debit' THEN ft.amount
			                     WHEN tt.type = 'debit' THEN tt.amount
			                     ELSE tt.amount END), 0)
		FROM links l
		JOIN transactions ft ON l.from_txn_id = ft.id AND ft.user_id = l.user_id
		JOIN accounts fa ON ft.account_id = fa.id
		JOIN transactions tt ON l.to_txn_id = tt.id AND tt.user_id = l.user_id
		JOIN accounts ta ON tt.account_id = ta.id
		WHERE l.user_id = $1`+either+`
		GROUP BY l.type
		ORDER BY l.type`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []flowLinkRow
	for rows.Next() {
		var r flowLinkRow
		if err := rows.Scan(&r.typ, &r.count, &r.total); err != nil {
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
func queryAccountLinkFlows(ctx context.Context, db flowQueryer, userID uuid.UUID, dateFrom, dateTo, accountID string) ([]flowAcctLinkRow, error) {
	fromCond, fromArgs, next := flowFilter("ft", "fa", 2, dateFrom, dateTo, accountID)
	toCond, toArgs, _ := flowFilter("tt", "ta", next, dateFrom, dateTo, accountID)
	either := combineFlowConds(fromCond, toCond)

	args := append([]any{userID}, fromArgs...)
	args = append(args, toArgs...)

	rows, err := db.Query(ctx, `
		SELECT ft.type, tt.type,
			   fa.id::text, fa.name, fa.color,
			   ta.id::text, ta.name, ta.color,
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
			&r.amount); err != nil {
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

// accountFlowEdges turns raw cross-account links into directed account flows
// (debit account -> credit account), then nets reciprocal pairs and drops the
// remaining back edges so the result is a DAG the Sankey can render. The
// processing order is deterministic (sorted ids), so the same input always
// yields the same edges.
func accountFlowEdges(rows []flowAcctLinkRow) []flowAccountEdge {
	edges, _ := analyzeAccountFlows(rows)
	return edges
}

// flowCycleEdge is one directed account leg of a circular flow.
type flowCycleEdge struct {
	srcID, dstID string
	value        money.Amount
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
// The cycles this discards are returned alongside them, so the circular-money
// report (GetLinkCycles) can surface exactly what the graph had to hide. The
// processing order is deterministic (sorted ids), so the same input always
// yields the same edges and the same cycles.
func analyzeAccountFlows(rows []flowAcctLinkRow) ([]flowAccountEdge, []flowCycle) {
	agg := map[[2]string]*flowAccountEdge{}
	for _, r := range rows {
		key, src, dst, ok := flowLinkEnds(r)
		if !ok {
			continue
		}
		if e, ok := agg[key]; ok {
			e.value += r.amount
			continue
		}
		agg[key] = &flowAccountEdge{
			srcID: src.id, srcName: src.name, srcColor: src.color,
			dstID: dst.id, dstName: dst.name, dstColor: dst.color,
			value: r.amount,
		}
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
		cycles = append(cycles, flowCycle{
			kind:  "reciprocal",
			nodes: []string{key[0], key[1]},
			legs: []flowCycleEdge{
				{srcID: key[0], dstID: key[1], value: edge.value},
				{srcID: key[1], dstID: key[0], value: revEdge.value},
			},
		})
		switch {
		case edge.value > revEdge.value:
			edge.value -= revEdge.value
			delete(agg, rev)
		case revEdge.value > edge.value:
			revEdge.value -= edge.value
			delete(agg, key)
		default:
			delete(agg, key)
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
	var visit func(string)
	visit = func(u string) {
		state[u] = 1
		stack = append(stack, u)
		for _, v := range adj[u] {
			edge, ok := agg[[2]string{u, v}]
			if !ok {
				continue
			}
			switch state[v] {
			case 1:
				// Back edge: dropping it breaks the cycle. The stack holds the
				// cycle's participants in flow order.
				if cycle, ok := flowStackCycle(agg, stack, v, edge.value); ok {
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

	sort.Slice(kept, func(i, j int) bool {
		if kept[i].srcID != kept[j].srcID {
			return kept[i].srcID < kept[j].srcID
		}
		return kept[i].dstID < kept[j].dstID
	})

	return kept, dedupeAndSortCycles(cycles)
}

// flowStackCycle builds the cycle closed by a back edge from the current DFS
// stack: v is the already-on-stack node the edge points back at, so the loop is
// stack[indexOf(v):] plus the closing leg. Legs carry the aggregated flow of
// each consecutive pair.
func flowStackCycle(agg map[[2]string]*flowAccountEdge, stack []string, v string, closing money.Amount) (flowCycle, bool) {
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
		edge, ok := agg[[2]string{nodes[i], nodes[i+1]}]
		if !ok {
			return flowCycle{}, false
		}
		legs = append(legs, flowCycleEdge{srcID: nodes[i], dstID: nodes[i+1], value: edge.value})
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

// flowFilter renders the shared date/account predicates for one table-alias
// pair, binding values into args and returning the rendered predicate (no
// leading conjunction) plus the arguments and the next free parameter index.
func flowFilter(dateAlias, accountAlias string, start int, dateFrom, dateTo, accountID string) (string, []any, int) {
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
func buildMoneyFlowGraph(incomeRows []flowIncomeRow, acctCatRows []flowAcctCatRow, catPayeeRows []flowCatPayeeRow, linkRows []flowLinkRow, acctLinkRows []flowAcctLinkRow, limit int) models.MoneyFlowGraph {
	incomeNodes := map[string]*models.MoneyFlowNode{}
	acctNodes := map[string]*models.MoneyFlowNode{}
	catNodes := map[string]*models.MoneyFlowNode{}
	payeeNodes := map[string]*models.MoneyFlowNode{}
	acctIn := map[string]money.Amount{}
	acctOut := map[string]money.Amount{}

	var totalIncome, totalExpense money.Amount

	addNode := func(m map[string]*models.MoneyFlowNode, kind, id, name, color, group string, amount money.Amount) {
		if n, ok := m[id]; ok {
			n.Total += amount
			return
		}
		m[id] = &models.MoneyFlowNode{ID: id, Name: name, Kind: kind, Color: color, Group: group, Total: amount}
	}

	// Income category color is the category's own swatch; the group is carried
	// for context. Account node totals are set below from the larger of their
	// inflow and outflow, so they are added with a zero amount here.
	for _, r := range incomeRows {
		color := r.catColor
		if color == "" {
			color = r.groupColor
		}
		addNode(incomeNodes, "income", flowIncomeNodeID(r.catID), r.catName, color, r.groupID, r.total)
		addNode(acctNodes, "account", flowAccountNodeID(r.acctID), r.acctName, r.acctColor, "", 0)
		acctIn[flowAccountNodeID(r.acctID)] += r.total
		totalIncome += r.total
	}

	for _, r := range acctCatRows {
		addNode(acctNodes, "account", flowAccountNodeID(r.acctID), r.acctName, r.acctColor, "", 0)
		acctOut[flowAccountNodeID(r.acctID)] += r.total
		totalExpense += r.total
	}

	// Category and payee totals both come from the category→payee grouping, so
	// each debit is counted once. Category nodes are colored by their base
	// group; the category's own swatch is the fallback.
	for _, r := range catPayeeRows {
		color := r.groupColor
		if color == "" {
			color = r.catColor
		}
		addNode(catNodes, "category", flowCategoryNodeID(r.catID), r.catName, color, r.groupID, r.total)
		addNode(payeeNodes, "payee", flowPayeeNodeID(r.payeeID), r.payeeName, "", "", r.total)
	}

	// Cross-account link flows (transfers, refunds, cashbacks, bill payments)
	// after netting and cycle-breaking so the account subgraph stays acyclic.
	// An endpoint may be an account with no other activity in the window, so
	// ensure its node exists before accumulating the link volume.
	acctEdges := accountFlowEdges(acctLinkRows)
	for _, e := range acctEdges {
		addNode(acctNodes, "account", flowAccountNodeID(e.srcID), e.srcName, e.srcColor, "", 0)
		addNode(acctNodes, "account", flowAccountNodeID(e.dstID), e.dstName, e.dstColor, "", 0)
		acctOut[flowAccountNodeID(e.srcID)] += e.value
		acctIn[flowAccountNodeID(e.dstID)] += e.value
	}

	// Account node volume is the larger of what flowed in and what flowed out,
	// so the node reads as the money that passed through it.
	for id, n := range acctNodes {
		n.Total = acctIn[id]
		if acctOut[id] > n.Total {
			n.Total = acctOut[id]
		}
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
		value          money.Amount
	}
	edges := map[string]*edge{}
	addEdge := func(source, target string, value money.Amount) {
		if value <= 0 {
			return
		}
		key := source + "\x00" + target
		if e, ok := edges[key]; ok {
			e.value += value
		} else {
			edges[key] = &edge{source: source, target: target, value: value}
		}
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
			r.total,
		)
	}
	for _, r := range acctCatRows {
		addEdge(
			flowAccountNodeID(r.acctID),
			rollup(flowCategoryNodeID(r.catID), catKept, "category:other"),
			r.total,
		)
	}
	for _, r := range catPayeeRows {
		addEdge(
			rollup(flowCategoryNodeID(r.catID), catKept, "category:other"),
			rollup(flowPayeeNodeID(r.payeeID), payeeKept, "payee:other"),
			r.total,
		)
	}
	for _, e := range acctEdges {
		addEdge(flowAccountNodeID(e.srcID), flowAccountNodeID(e.dstID), e.value)
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

	summary := make([]models.MoneyFlowLinkSummary, 0, len(linkRows))
	for _, r := range linkRows {
		summary = append(summary, models.MoneyFlowLinkSummary{Type: r.typ, Count: r.count, Total: r.total})
	}

	if nodes == nil {
		nodes = []models.MoneyFlowNode{}
	}

	return models.MoneyFlowGraph{
		Nodes:        nodes,
		Links:        links,
		TotalIncome:  totalIncome,
		TotalExpense: totalExpense,
		LinkSummary:  summary,
	}
}

// keepTopFlowNodes returns the ids of the limit highest-volume nodes. A nil or
// negative limit keeps nothing; limit >= len keeps everything.
func keepTopFlowNodes(m map[string]*models.MoneyFlowNode, limit int) map[string]bool {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if m[ids[i]].Total != m[ids[j]].Total {
			return m[ids[i]].Total > m[ids[j]].Total
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
		if m[ids[i]].Total != m[ids[j]].Total {
			return m[ids[i]].Total > m[ids[j]].Total
		}
		return ids[i] < ids[j]
	})

	out := make([]models.MoneyFlowNode, 0, len(ids)+1)
	for _, id := range ids {
		out = append(out, *m[id])
	}

	if otherID != "" && len(kept) < len(m) {
		other := models.MoneyFlowNode{ID: otherID, Name: otherName, Kind: kind}
		for id, n := range m {
			if kept[id] {
				continue
			}
			other.Total += n.Total
			if other.Color == "" {
				other.Color = n.Color
			}
		}
		out = append(out, other)
	}
	return out
}
