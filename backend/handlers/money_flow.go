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

// flowQueryer is the transactional read surface the flow queries use. *pgx.Tx
// satisfies it; keeping it narrow lets the query helpers stay testable.
type flowQueryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// GetMoneyFlow aggregates the user's transactions into a left-to-right Sankey
// graph (money sources → accounts → spending categories → payees) over an
// optional date range and account filter. The graph is acyclic by construction;
// transaction links (transfers, refunds, cashbacks, bill payments) are returned
// as a separate per-type summary so they can be surfaced beside the graph
// rather than as account-to-account edges, which would introduce cycles.
//
// All reads run in a single read-only, repeatable-read transaction so the four
// stages reflect one consistent snapshot.
func (srv *Server) GetMoneyFlow(c *gin.Context) {
	ctx := c
	userID := auth.GetUserID(c)
	dateFrom := c.Query("dateFrom")
	dateTo := c.Query("dateTo")
	accountID := c.Query("accountId")

	// Reject a malformed account id up front: the parameter is compared against
	// a uuid column, so a non-uuid would otherwise surface as a 500.
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

	graph := buildMoneyFlowGraph(incomeRows, acctCatRows, catPayeeRows, linkRows, limit)

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

	// A link is in scope when either endpoint matches; combine the two
	// predicate groups (each referencing its own bound parameters).
	either := ""
	switch {
	case fromCond == "" && toCond == "":
		either = ""
	case fromCond == "":
		either = " AND (" + toCond + ")"
	case toCond == "":
		either = " AND (" + fromCond + ")"
	default:
		either = " AND ((" + fromCond + ") OR (" + toCond + "))"
	}

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
func buildMoneyFlowGraph(incomeRows []flowIncomeRow, acctCatRows []flowAcctCatRow, catPayeeRows []flowCatPayeeRow, linkRows []flowLinkRow, limit int) models.MoneyFlowGraph {
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
