package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"sort"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/internal/money"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// flowLinkDetailRow is one raw cross-account link plus its own link type — the
// extra field the circular-money report needs to break an account flow down by
// link type (transfer/cashback/refund/bill_payment).
type flowLinkDetailRow struct {
	flowAcctLinkRow
	linkType string
}

// flowPairTotal is the pre-netting rollup of one directed account pair: every
// link moving money from src to dst in the window, split by link type.
type flowPairTotal struct {
	src, dst flowAccountEnd
	total    money.Amount
	count    int
	types    map[string]*models.LinkFlowTypeTotal
}

// GetLinkCycles reports the account-to-account link flows the Money Flow Sankey
// cannot draw. The graph must stay acyclic, so `accountFlowEdges` nets
// reciprocal pairs and drops the DFS back edges that close a longer loop — and
// those discarded cycles are themselves a diagnostic ("is one card paying
// another in a loop?"). The report returns them with their participants, each
// leg's gross flow, and the amount that actually circulates the whole loop,
// plus the directed account flows that have no counterpart in the opposite
// direction (a half-entered transfer looks exactly like a one-way bill
// payment). Filters mirror `GET /dashboard/money-flow`: `dateFrom`/`dateTo`
// bound the window and `accountId` keeps links with either endpoint on that
// account.
func (srv *Server) GetLinkCycles(c *gin.Context) {
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

	rows, err := queryAccountLinkDetails(c, srv.db, userID, dateFrom, dateTo, accountID)
	if err != nil {
		slog.Error("GetLinkCycles", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, buildLinkCycleReport(rows))
}

// queryAccountLinkDetails reads every cross-account link in the window with the
// link's own type, so the report can both rebuild the account graph (via
// flowLinkEnds) and label each leg. It shares the endpoint predicate builder
// with the money-flow queries, so the report and the Sankey always see the same
// links.
func queryAccountLinkDetails(ctx context.Context, db flowQueryer, userID uuid.UUID, dateFrom, dateTo, accountID string) ([]flowLinkDetailRow, error) {
	fromCond, fromArgs, next := flowFilter("ft", "fa", 2, dateFrom, dateTo, accountID)
	toCond, toArgs, _ := flowFilter("tt", "ta", next, dateFrom, dateTo, accountID)
	either := combineFlowConds(fromCond, toCond)

	args := append([]any{userID}, fromArgs...)
	args = append(args, toArgs...)

	rows, err := db.Query(ctx, `
		SELECT l.type, ft.type, tt.type,
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

	var out []flowLinkDetailRow
	for rows.Next() {
		var r flowLinkDetailRow
		if err := rows.Scan(&r.linkType, &r.fromType, &r.toType,
			&r.fromAcctID, &r.fromAcctName, &r.fromAcctColor,
			&r.toAcctID, &r.toAcctName, &r.toAcctColor,
			&r.amount); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// buildLinkCycleReport turns the raw cross-account links into the report: the
// cycles the Sankey's cycle-breaking discards, and the one-directional account
// flows. Amounts are the gross flows in the window (pre-netting), so a leg's
// total always agrees with the per-type rollup underneath it; a cycle's Net is
// the smallest leg, the amount that fully circulates the loop.
func buildLinkCycleReport(rows []flowLinkDetailRow) models.LinkCycleReport {
	report := models.LinkCycleReport{
		Cycles:        []models.LinkCycle{},
		OneSidedFlows: []models.LinkOneSidedFlow{},
	}

	minimal := make([]flowAcctLinkRow, 0, len(rows))
	pairs := map[[2]string]*flowPairTotal{}
	accounts := map[string]flowAccountEnd{}
	for _, r := range rows {
		minimal = append(minimal, r.flowAcctLinkRow)
		key, src, dst, ok := flowLinkEnds(r.flowAcctLinkRow)
		if !ok {
			continue
		}
		accounts[src.id] = src
		accounts[dst.id] = dst

		pair := pairs[key]
		if pair == nil {
			pair = &flowPairTotal{src: src, dst: dst, types: map[string]*models.LinkFlowTypeTotal{}}
			pairs[key] = pair
		}
		pair.total += r.amount
		pair.count++
		byType := pair.types[r.linkType]
		if byType == nil {
			byType = &models.LinkFlowTypeTotal{Type: r.linkType}
			pair.types[r.linkType] = byType
		}
		byType.Count++
		byType.Total += r.amount
	}

	_, cycles := analyzeAccountFlows(minimal)
	// A cycle's own legs have no reverse flow either, but they are the
	// counterpart of one another; only flows outside every cycle are one-sided.
	inCycle := map[[2]string]bool{}
	for _, cycle := range cycles {
		out := models.LinkCycle{
			Kind:     cycle.kind,
			Accounts: make([]models.LinkCycleAccount, 0, len(cycle.nodes)),
			Legs:     make([]models.LinkCycleLeg, 0, len(cycle.legs)),
		}
		for _, id := range cycle.nodes {
			account := accounts[id]
			out.Accounts = append(out.Accounts, models.LinkCycleAccount{
				ID: account.id, Name: account.name, Color: account.color,
			})
		}
		net := money.Amount(0)
		for i, leg := range cycle.legs {
			key := [2]string{leg.srcID, leg.dstID}
			inCycle[key] = true
			pair := pairs[key]
			if pair == nil {
				continue
			}
			out.Legs = append(out.Legs, models.LinkCycleLeg{
				FromAccountID:    pair.src.id,
				FromAccountName:  pair.src.name,
				FromAccountColor: pair.src.color,
				ToAccountID:      pair.dst.id,
				ToAccountName:    pair.dst.name,
				ToAccountColor:   pair.dst.color,
				Amount:           pair.total,
				Count:            pair.count,
				Types:            sortedFlowTypes(pair.types),
			})
			out.Gross += pair.total
			out.Transactions += pair.count
			if i == 0 || pair.total < net {
				net = pair.total
			}
		}
		out.Net = net
		report.TotalCircular += net
		report.Cycles = append(report.Cycles, out)
	}

	for key, pair := range pairs {
		if inCycle[key] {
			continue
		}
		if _, ok := pairs[[2]string{key[1], key[0]}]; ok {
			continue
		}
		report.OneSidedFlows = append(report.OneSidedFlows, models.LinkOneSidedFlow{
			FromAccountID:    pair.src.id,
			FromAccountName:  pair.src.name,
			FromAccountColor: pair.src.color,
			ToAccountID:      pair.dst.id,
			ToAccountName:    pair.dst.name,
			ToAccountColor:   pair.dst.color,
			Total:            pair.total,
			Count:            pair.count,
			Types:            sortedFlowTypes(pair.types),
		})
	}
	sort.Slice(report.OneSidedFlows, func(i, j int) bool {
		if report.OneSidedFlows[i].FromAccountID != report.OneSidedFlows[j].FromAccountID {
			return report.OneSidedFlows[i].FromAccountID < report.OneSidedFlows[j].FromAccountID
		}
		return report.OneSidedFlows[i].ToAccountID < report.OneSidedFlows[j].ToAccountID
	})

	return report
}

// sortedFlowTypes flattens a per-type rollup for display, largest flow first
// (ties broken by type name so the order is deterministic).
func sortedFlowTypes(types map[string]*models.LinkFlowTypeTotal) []models.LinkFlowTypeTotal {
	out := make([]models.LinkFlowTypeTotal, 0, len(types))
	for _, t := range types {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		return out[i].Type < out[j].Type
	})
	return out
}
