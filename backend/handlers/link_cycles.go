package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"sort"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The diagnostics half of the money-flow graph: what a Sankey cannot draw.
//
// money_flow.go has to keep the account-to-account subgraph acyclic, because a
// cyclic Sankey is unreadable. It gets there by netting reciprocal pairs into
// one edge and dropping DFS back edges — both of which *hide* real flows. This
// file is the counterpart: it reports exactly what was netted away or broken, so
// the user can see money bouncing between accounts or a transfer that was only
// half entered.
//
// That is the design contract between the two files, and it is why the graph
// never has to apologise for what it dropped: netting and cycle-breaking are not
// lossy here, they are deferred to a dedicated read. GetLinkCycles re-derives
// the same account pairs from the same link rows and reports them un-netted.
//
// Two kinds of finding come out, and they mean different things:
//
//	reciprocal  a pair that flows both ways. Legitimate (a credit card paying
//	            into a bank and spending back out) but the graph can only show
//	            the net.
//	cycle       a longer loop that had to be broken. More often a symptom of a
//	            half-entered transfer or a duplicate link than of real circular
//	            money moving.
//
// flowPairTotal is per-currency for the same reason everything else in this
// package is: a pair is two accounts, and a pair can hold a link in each of two
// currencies. The reverse pair is a distinct key, which is what lets the two
// collapse into one reciprocal finding without adding across currencies.

// flowLinkDetailRow is one raw cross-account link plus its own link type — the
// extra field the circular-money report needs to break an account flow down by
// link type (transfer/cashback/refund/bill_payment).
type flowLinkDetailRow struct {
	flowAcctLinkRow
	linkType string
}

// flowPairTotal is the pre-netting rollup of one directed account pair: every
// link moving money from src to dst in the window, split by link type. total is
// per-currency, and must be: a pair is two accounts, and two accounts need not
// share a currency. A single pair can therefore hold a link in each of two
// currencies, and the reverse pair — a different key — is what turns the two
// into a reciprocal cycle.
type flowPairTotal struct {
	src, dst flowAccountEnd
	total    models.CurrencyAmounts
	count    int
	types    map[string]*models.LinkFlowTypeTotal
}

// GetLinkCycles reports the account-to-account link flows the Money Flow Sankey
// cannot draw. The graph must stay acyclic, so `analyzeAccountFlows` nets
// reciprocal pairs and drops the DFS back edges that close a longer loop — and
// those discarded cycles are themselves a diagnostic ("is one card paying
// another in a loop?"). The report returns them with their participants, each
// leg's gross flow, and the amount that actually circulates the whole loop,
// plus the directed account flows that have no counterpart in the opposite
// direction (a half-entered transfer looks exactly like a one-way bill
// payment). Filters mirror `GET /dashboard/money-flow`: `dateFrom`/`dateTo`
// bound the window, `accountId` keeps links with either endpoint on that account
// and `currency` keeps the ones whose amount is denominated in that code.
//
// Every amount is per-currency, and this endpoint has the least room to be
// otherwise: a link has a from-account and a to-account, so it sums two
// accounts' transactions by construction and no account filter avoids the
// question. A cycle whose legs are all in one currency reports the circulation
// figure it always did; a cycle spanning currencies reports each currency's own
// smallest leg, which is a local figure and not a circulation one — see
// models.LinkCycle.Net, which states which is which. The currencyScope names the
// accounts holding each currency in the response, so under ?currency=USD it
// lists the USD ones, and a reported leg's other endpoint may be an account it
// does not list: the filter narrows on the currency of the amount, which is not
// necessarily either endpoint's account currency.
func (srv *Server) GetLinkCycles(c *gin.Context) {
	ctx := c
	userID := auth.GetUserID(c)
	accountID := c.Query("accountId")

	// Reject a malformed account id up front: the parameter is compared against
	// a uuid column, so a non-uuid would otherwise surface as a 500.
	if accountID != "" {
		if _, err := uuid.Parse(accountID); err != nil {
			validation.RespondError(c, "invalid accountId", http.StatusBadRequest)
			return
		}
	}

	// The window bounds are compared against a date column, so they get the
	// same 400 the money-flow endpoints sharing this helper already return
	// instead of a Postgres parse error surfacing as a 500.
	dateFrom, ok := parseQueryDate(c, "dateFrom", c.Query("dateFrom"))
	if !ok {
		return
	}
	dateTo, ok := parseQueryDate(c, "dateTo", c.Query("dateTo"))
	if !ok {
		return
	}

	// The currency is parsed once here and passed to both reads, so the scope
	// query and the link query narrow on the same normalised code.
	currency, ok := parseCurrency(c)
	if !ok {
		return
	}

	// The scope and the links are read in one snapshot. The scope is the report's
	// own account of which currencies it covers, so a write landing between two
	// autocommit statements could have a cycle reported beside an account list
	// that does not hold it — which is the silent gap this response exists to
	// close. Same argument, and same shape, as the dashboard summary, the
	// money-flow graph and the flow timeline.
	tx, err := srv.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		slog.Error("GetLinkCycles (begin)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(ctx)

	scope, err := srv.currencyScope(ctx, tx, userID, scopeOptions{
		DateFrom:  dateFrom,
		DateTo:    dateTo,
		AccountID: accountID,
		Currency:  currency,
	})
	if err != nil {
		slog.Error("GetLinkCycles (currency scope)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	rows, err := queryAccountLinkDetails(ctx, tx, userID, dateFrom, dateTo, accountID, currency)
	if err != nil {
		slog.Error("GetLinkCycles", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		slog.Error("GetLinkCycles (commit)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	report := buildLinkCycleReport(rows)
	report.CurrencyScope = scope.Scope
	c.JSON(http.StatusOK, report)
}

// queryAccountLinkDetails reads every cross-account link in the window with the
// link's own type, so the report can both rebuild the account graph (via
// flowLinkEnds) and label each leg. It shares the endpoint predicate builder
// with the money-flow queries, so the report and the Sankey always see the same
// links.
//
// The currency is the one the money-flow link queries use, and for the same
// reason: a link's value is whichever of its two transactions' amounts the CASE
// picked, and a transaction's amount is always denominated in its own account's
// currency because transactions carry no currency column of their own. So the
// leg carries the currency of the account the amount came *from* — which is not
// necessarily the from-account, since a link whose stored orientation is
// credit-then-debit takes its amount from the second endpoint. See
// linkCurrencyColumn in money_flow.go, which is the one definition of the
// expression and states the premise in full; the ?currency= filter narrows on
// that same currency rather than on either endpoint's account, so a link is not
// admitted by one endpoint's currency and then reported in the other's.
func queryAccountLinkDetails(ctx context.Context, db flowQueryer, userID uuid.UUID, dateFrom, dateTo, accountID, currency string) ([]flowLinkDetailRow, error) {
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
		SELECT l.type, ft.type, tt.type,
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

	var out []flowLinkDetailRow
	for rows.Next() {
		var r flowLinkDetailRow
		if err := rows.Scan(&r.linkType, &r.fromType, &r.toType,
			&r.fromAcctID, &r.fromAcctName, &r.fromAcctColor,
			&r.toAcctID, &r.toAcctName, &r.toAcctColor,
			&r.currency, &r.amount); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// buildLinkCycleReport turns the raw cross-account links into the report: the
// cycles the Sankey's cycle-breaking discards, and the one-directional account
// flows. Amounts are the gross flows in the window (pre-netting), so a leg's
// total always agrees with the per-type rollup underneath it; a cycle's Net is
// the smallest leg within each currency — see models.LinkCycle for why that is
// the smallest *within a currency* and what a multi-currency cycle therefore
// does not report.
func buildLinkCycleReport(rows []flowLinkDetailRow) models.LinkCycleReport {
	report := models.LinkCycleReport{
		Cycles:        []models.LinkCycle{},
		OneSidedFlows: []models.LinkOneSidedFlow{},
		TotalCircular: models.NewCurrencyAmounts(),
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
			// Both maps start non-nil: a pair whose links are all zero-valued
			// still has to serialize as {} rather than null, and Add leaves an
			// empty map empty rather than allocating one.
			pair = &flowPairTotal{
				src: src, dst: dst,
				total: models.NewCurrencyAmounts(),
				types: map[string]*models.LinkFlowTypeTotal{},
			}
			pairs[key] = pair
		}
		pair.total = pair.total.Add(r.currency, r.amount)
		pair.count++
		byType := pair.types[r.linkType]
		if byType == nil {
			byType = &models.LinkFlowTypeTotal{Type: r.linkType, Total: models.NewCurrencyAmounts()}
			pair.types[r.linkType] = byType
		}
		byType.Count++
		byType.Total = byType.Total.Add(r.currency, r.amount)
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
			Net:      models.NewCurrencyAmounts(),
			Gross:    models.NewCurrencyAmounts(),
		}
		for _, id := range cycle.nodes {
			account := accounts[id]
			out.Accounts = append(out.Accounts, models.LinkCycleAccount{
				ID: account.id, Name: account.name, Color: account.color,
			})
		}
		for _, leg := range cycle.legs {
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
			out.Transactions += pair.count
			for code, amount := range pair.total {
				out.Gross = out.Gross.Add(code, amount)
			}
			// No seeding pass: the first leg folds against an empty Net, and
			// minCycleAmounts' union rule hands back exactly that leg's keys, so
			// the first leg is the starting value rather than a comparison
			// against an invented zero.
			out.Net = minCycleAmounts(out.Net, pair.total)
		}
		for code, amount := range out.Net {
			report.TotalCircular = report.TotalCircular.Add(code, amount)
		}
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

// minCycleAmounts takes the smaller of two per-currency totals, one currency at
// a time, over the union of their keys. The comparison happens *inside* a
// currency and nowhere else, which is the whole point and the thing this code
// looks as though it does not need: two maps cannot be ordered by "smaller", and
// the obvious reading of a per-currency minimum — take the smallest number
// whichever currency it is denominated in — is the cross-currency total this
// endpoint exists to refuse. A currency only one operand holds is its own
// minimum, the same union rule Sub and maxFlowAmounts follow, which is also why
// the first leg needs no special case: folded against an empty map it comes back
// unchanged. See models.LinkCycle.Net for which of these figures is a
// circulation figure, and when.
func minCycleAmounts(a, b models.CurrencyAmounts) models.CurrencyAmounts {
	out := models.NewCurrencyAmounts()
	for code, amount := range a {
		if other, ok := b[code]; ok && other < amount {
			amount = other
		}
		out[code] = amount
	}
	for code, amount := range b {
		if _, ok := a[code]; !ok {
			out[code] = amount
		}
	}
	return out
}

// sortedFlowTypes flattens a per-type rollup for display, largest flow first
// (ties broken by type name so the order is deterministic). "Largest" is
// compareFlowTotals' order over the per-currency totals: with one currency it is
// the amount as before, and with two it is a total order over the sorted union
// of the key sets rather than a comparison between a rupee and a dollar.
func sortedFlowTypes(types map[string]*models.LinkFlowTypeTotal) []models.LinkFlowTypeTotal {
	out := make([]models.LinkFlowTypeTotal, 0, len(types))
	for _, t := range types {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool {
		if c := compareFlowTotals(out[i].Total, out[j].Total); c != 0 {
			return c < 0
		}
		return out[i].Type < out[j].Type
	})
	return out
}
