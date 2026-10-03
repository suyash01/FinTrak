package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/internal/money"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The period strip beneath the Money Flow Sankey — a row of clickable periods
// that scrub the graph's window.
//
// It exists because the Sankey aggregates the *whole* window into one graph.
// This endpoint is what lets the user narrow that window without re-deriving it
// blind: each cell carries the income/expense/net for one period, so the strip
// is both the scrubber and a coarse chart of the same data.
//
// It is a GET that materializes: it reaches ensureBillingCycles in the
// billing-cycle branch, which is why /dashboard/money-flow and this route are in
// both crossSiteGetGuard and readonly.SideEffectingGETs. All reads run in one
// repeatable-read transaction so the strip and the graph it scrubs describe the
// same snapshot.
//
// Note the split from the Sankey itself: this is per-period, and the graph is
// per-window. Changing the strip's selection narrows the graph's filters — it
// does not filter the Sankey server-side per node.

// GetMoneyFlowTimeline returns per-period income/expense/net for the Money Flow
// page's timeline strip, so a period can be clicked to scrub the Sankey window.
// Periods are calendar months by default; with groupBy=billing_cycle (which
// requires a single account that has a billing day) they are the account's
// statement periods instead.
//
// Every amount is per-currency. The monthly view is transaction-driven and can
// cover an INR account and a USD one, so a month is one period holding one amount
// per currency, and the response names the accounts behind them. A period's net is
// the difference inside one currency, computed by the server: that difference is
// meaningful even when the period as a whole spans several, and no total is ever
// taken across two.
func (srv *Server) GetMoneyFlowTimeline(c *gin.Context) {
	ctx := c
	userID := auth.GetUserID(c)
	dateFrom := c.Query("dateFrom")
	dateTo := c.Query("dateTo")
	accountID := c.Query("accountId")
	groupBy := c.DefaultQuery("groupBy", "month")

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

	// The currency is parsed once here and passed down, so the scope query and
	// the period query narrow on the same normalised code in both views.
	currency, ok := parseCurrency(c)
	if !ok {
		return
	}

	if groupBy == billingCycleGroupBy {
		srv.getMoneyFlowTimelineBillingCycle(c, currency)
		return
	}

	// The scope query names the currencies the periods below are reported in. It
	// reads accounts rather than transactions, so an account quiet in the window
	// still appears and explains the currency it holds - which is what lets a
	// caller see a currency holding no transaction in the window at all, rather
	// than a period list silently missing it.
	//
	// Both reads run in one read-only, repeatable-read transaction. The scope is
	// the periods' own explanation of themselves, so a write landing between two
	// autocommit statements would let them describe different ledgers - an
	// account's scoped income and its period's amount disagreeing, or a period
	// naming a currency the scope never mentions, which is the silent gap this
	// response exists to close. Same argument, and same shape, as the dashboard
	// summary and the money-flow graph.
	tx, err := srv.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		slog.Error("GetMoneyFlowTimeline (begin)", slog.String("error", err.Error()))
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
		slog.Error("GetMoneyFlowTimeline (currency scope)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	periods, err := queryMonthlyFlowTimeline(ctx, tx, userID, dateFrom, dateTo, accountID, currency)
	if err != nil {
		slog.Error("GetMoneyFlowTimeline (monthly)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		slog.Error("GetMoneyFlowTimeline (commit)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, models.MoneyFlowTimeline{
		GroupBy:       "month",
		Periods:       periods,
		CurrencyScope: scope.Scope,
	})
}

// queryMonthlyFlowTimeline groups the filtered transactions into calendar
// months, returning the inclusive month bounds the client re-queries with.
//
// The account's currency is projected and grouped with the month, so one month is
// one period holding one amount per currency rather than one period per currency.
// Reading the transaction's denomination off its account rests on there being no
// currency of its own on a transaction: a transaction's amount is always in its own
// account's currency, and a schema that gave transactions a currency would make this
// projection a mislabel rather than an error. The same premise is stated for links
// in money_flow.go's linkCurrencyColumn.
//
// The grouping is by ordinal: the currency is the second select expression, so
// `GROUP BY 1, 2` reads the month and the currency, and `ORDER BY 1, 2` returns
// each month in one piece with its currencies in a fixed order. Grouping the month
// without the currency is what added dollars to rupees.
func queryMonthlyFlowTimeline(ctx context.Context, db cycleQueryer, userID uuid.UUID, dateFrom, dateTo, accountID, currency string) ([]models.MoneyFlowTimelinePeriod, error) {
	cond, args, _ := flowFilter("t", "a", 2, dateFrom, dateTo, accountID, currency)
	filter := ""
	if cond != "" {
		filter = " AND " + cond
	}

	rows, err := db.Query(ctx, `
		SELECT date_trunc('month', t.date)::date,
			   `+flowCurrency("a.currency")+` AS currency,
			   COALESCE(SUM(CASE WHEN t.type = 'credit' THEN t.amount ELSE 0 END), 0),
			   COALESCE(SUM(CASE WHEN t.type = 'debit' THEN t.amount ELSE 0 END), 0)
		FROM transactions t
		JOIN accounts a ON t.account_id = a.id
		WHERE t.user_id = $1`+filter+`
		GROUP BY 1, 2
		ORDER BY 1, 2`, append([]any{userID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// The rows arrive in month order, one per month per currency, so the fold
	// keeps the months it has seen in the order it saw them.
	byKey := map[string]*models.MoneyFlowTimelinePeriod{}
	var keys []string
	for rows.Next() {
		var start time.Time
		var code string
		var income, expense money.Amount
		if err := rows.Scan(&start, &code, &income, &expense); err != nil {
			return nil, err
		}
		start = time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC)
		key := start.Format("2006-01")
		entry, ok := byKey[key]
		if !ok {
			end := time.Date(start.Year(), start.Month()+1, 0, 0, 0, 0, 0, time.UTC)
			entry = &models.MoneyFlowTimelinePeriod{
				Key:       key,
				Label:     start.Format("Jan 2006"),
				StartDate: start.Format("2006-01-02"),
				EndDate:   end.Format("2006-01-02"),
				Income:    models.NewCurrencyAmounts(),
				Expense:   models.NewCurrencyAmounts(),
			}
			byKey[key] = entry
			keys = append(keys, key)
		}
		entry.Income.Add(code, income)
		entry.Expense.Add(code, expense)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	periods := make([]models.MoneyFlowTimelinePeriod, 0, len(keys))
	for _, key := range keys {
		entry := byKey[key]
		// Net is the difference inside one currency, and the server takes it: a
		// client subtracting two currency-keyed objects by hand is how a number
		// across two currencies comes back. Sub takes the union of the keys and
		// reads a missing one as zero, so a month that only spent still reports
		// its negative net under the currency it spent in.
		entry.Net = entry.Income.Sub(entry.Expense)
		periods = append(periods, *entry)
	}
	return periods, nil
}

// getMoneyFlowTimelineBillingCycle frames the timeline around the statement
// periods of one billing-day account, mirroring the dashboard's billing-cycle
// window: the last `cycles` cycles ending with the current one. `currency` is the
// already-normalised ?currency filter, parsed by the caller so both views narrow
// on the same value.
func (srv *Server) getMoneyFlowTimelineBillingCycle(c *gin.Context, currency string) {
	ctx := c
	userID := auth.GetUserID(c)

	accountID, err := uuid.Parse(c.Query("accountId"))
	if err != nil {
		validation.RespondError(c, "accountId is required for billing cycle view", http.StatusBadRequest)
		return
	}

	// Only the billing day is read here, and it has to be: ensureBillingCycles
	// below needs it, and that runs before the snapshot this branch's other reads
	// share. The account's currency is deliberately not read alongside it, because
	// it keys every period of the response and therefore has to come from the same
	// snapshot as the scope that names it - see the read inside the transaction
	// below. This is the branch's own rule (reads whose results must agree share
	// one transaction), applied everywhere except here until now.
	var billingDay *int
	err = srv.db.QueryRow(ctx,
		`SELECT a.billing_day
		 FROM accounts a WHERE a.id = $1 AND a.user_id = $2`,
		accountID, userID).Scan(&billingDay)
	if errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "account not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("GetMoneyFlowTimeline (account lookup)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if billingDay == nil {
		validation.RespondError(c, "account has no billing day", http.StatusBadRequest)
		return
	}

	// The regeneration detaches and recreates the account's cycles, so it runs
	// in one transaction: an interruption between the detach and the delete
	// would otherwise leave its transactions detached from cycles that still
	// exist.
	if err := db.WithTx(ctx, srv.db, func(tx pgx.Tx) error {
		return ensureBillingCycles(ctx, tx, userID, accountID, *billingDay)
	}); err != nil {
		slog.Error("GetMoneyFlowTimeline (ensure billing cycles)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// The cycle list is read on the pool rather than in the snapshot below, and
	// unlike the dashboard's equivalent this is an ordering constraint rather than
	// an oversight: the window is derived from these cycles, and the window is an
	// argument to the scope query that opens the snapshot. The snapshot cannot be
	// asked anything until the list has come back, so the list cannot be part of
	// it. What the list contributes to the response is the window's bounds, which
	// is why the read that decides the *key* of every amount - the account's
	// currency - is not here.
	cycles, err := listBillingCycles(ctx, srv.db, userID, accountID)
	if err != nil {
		slog.Error("GetMoneyFlowTimeline (list billing cycles)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if len(cycles) == 0 {
		// No cycle means no window, so the response covers no currency rather
		// than an unnamed one: both arrays are empty, never null.
		c.JSON(http.StatusOK, models.MoneyFlowTimeline{
			GroupBy:       billingCycleGroupBy,
			Periods:       []models.MoneyFlowTimelinePeriod{},
			CurrencyScope: models.CurrencyScope{Currencies: []string{}, Accounts: []models.ScopedAccount{}},
		})
		return
	}

	numCycles := defaultBillingCycles
	if raw := c.Query("cycles"); raw != "" {
		if n, perr := strconv.Atoi(raw); perr == nil && n > 0 {
			if n > maxBillingCycles {
				n = maxBillingCycles
			}
			numCycles = n
		}
	}
	window := cycles
	if len(cycles) > numCycles {
		window = cycles[len(cycles)-numCycles:]
	}
	windowStart := window[0].StartDate.Format("2006-01-02")
	windowEnd := window[len(window)-1].EndDate.Format("2006-01-02")

	// The scope and the periods share one read-only, repeatable-read snapshot, for
	// the same reason as the monthly view: the scope names the currency the
	// periods are in, so it has to be read from the same ledger they were. It
	// starts here rather than around the whole branch because ensureBillingCycles
	// above is a writer, and a read-only transaction could not contain it.
	tx, err := srv.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		slog.Error("GetMoneyFlowTimeline (begin)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(ctx)

	// The account's currency, read here rather than in the lookup above. It keys
	// every period in the response, so a concurrent edit between the two reads
	// would key the periods by a currency the scope below - in the same snapshot -
	// does not name: the response would hold one amount per period in a currency
	// its own scope never mentions, which is the same defect as a scope read by
	// date beside periods joined by cycle id, arrived at from the other side.
	//
	// The read is once, not per period, for the reason the monthly view's
	// currency-column comment gives: a cycle belongs to exactly one account and
	// the API only lets a transaction be assigned to a cycle of its own account,
	// so this window cannot span currencies. The maps below are per-currency for
	// consistency with the monthly view, not because the keys vary. And the
	// account's currency is the transactions' denomination only because a
	// transaction carries none of its own.
	var accountCurrency string
	err = tx.QueryRow(ctx,
		`SELECT `+flowCurrency("a.currency")+`
		 FROM accounts a WHERE a.id = $1 AND a.user_id = $2`,
		accountID, userID).Scan(&accountCurrency)
	if errors.Is(err, pgx.ErrNoRows) {
		// The account existed a moment ago and the snapshot began after that read.
		// Its row is gone now, so the periods and the scope below have nothing to
		// describe; 404 is the honest answer rather than a period keyed by a
		// currency this branch would have to invent.
		validation.RespondError(c, "account not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("GetMoneyFlowTimeline (account currency)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	scope, err := srv.currencyScope(ctx, tx, userID, scopeOptions{
		DateFrom:  windowStart,
		DateTo:    windowEnd,
		AccountID: accountID.String(),
		Currency:  currency,
	})
	if err != nil {
		slog.Error("GetMoneyFlowTimeline (currency scope)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// The ?currency= filter narrows the periods as well as the scope, so the two
	// cannot disagree about which currencies are in the response. The accounts
	// join is on the cycle's own account rather than on the transaction's, so it
	// is an inner join that drops no cycle - joining through the transaction would
	// turn the LEFT JOIN below into an inner one and lose every cycle with no
	// transactions in it. The predicate sits in the WHERE, which is where a
	// transaction-driven filter belongs; the "never drop a quiet account" rule
	// belongs to the account-driven scope query, and lives in currencyScope.
	cycleFilter := ""
	cycleArgs := []any{accountID, userID, windowStart, windowEnd}
	if currency != "" {
		cycleFilter = " AND " + flowCurrencyPredicate("a.currency", len(cycleArgs)+1)
		cycleArgs = append(cycleArgs, currency)
	}

	// The transactions are joined by the cycle's own date range rather than by
	// t.billing_cycle_id, which is the dashboard's per-cycle trend join for the
	// dashboard's reason: the scope above is read by date, so a period joined by
	// cycle id would describe a different set of transactions than the scope
	// beside it. A transaction detached from its cycle by hand - PATCH
	// /transactions/{id} with {"billingCycleId": null}, which
	// billing_cycle_detached makes permanent - is then in the scope's income and
	// expense and in no period at all, so one account over one window reports two
	// figures and nothing in the response reconciles them. Before this branch the
	// timeline carried no totals to disagree with, which is why only the dashboard
	// was fixed the first time. Do not "optimise" the join back to the cycle id.
	rows, err := tx.Query(ctx, `
		SELECT bc.id, bc.start_date, bc.end_date, bc.label,
			   COALESCE(SUM(CASE WHEN t.type = 'credit' THEN t.amount ELSE 0 END), 0),
			   COALESCE(SUM(CASE WHEN t.type = 'debit' THEN t.amount ELSE 0 END), 0)
		FROM billing_cycles bc
		JOIN accounts a ON a.id = bc.account_id AND a.user_id = $2
		LEFT JOIN transactions t ON t.account_id = bc.account_id AND t.user_id = $2
		   AND t.date >= bc.start_date AND t.date <= bc.end_date
		WHERE bc.account_id = $1 AND bc.user_id = $2
		  AND bc.end_date >= $3 AND bc.end_date <= $4`+cycleFilter+`
		GROUP BY bc.id, bc.start_date, bc.end_date, bc.label
		ORDER BY bc.start_date ASC`, cycleArgs...)
	if err != nil {
		slog.Error("GetMoneyFlowTimeline (cycle periods)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	periods, err := billingCycleTimelinePeriods(rows, accountCurrency)
	if err != nil {
		slog.Error("GetMoneyFlowTimeline (cycle periods fold)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		slog.Error("GetMoneyFlowTimeline (commit)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, models.MoneyFlowTimeline{
		GroupBy:       billingCycleGroupBy,
		Periods:       periods,
		CurrencyScope: scope.Scope,
	})
}

// billingCycleTimelinePeriods folds the cycle rows into one period per cycle, each
// in the account's single currency. It is a function rather than inline code in the
// handler so that its row-stream error is testable on its own: through the handler
// a failed stream and a failed commit are both a 500, so a test there cannot tell
// which branch answered, and a fold that dropped the rows.Err() check would pass
// it. Half-read folds report the cycles that did arrive and silently omit the rest,
// which reads as a quiet account rather than as a failed read.
func billingCycleTimelinePeriods(rows pgx.Rows, accountCurrency string) ([]models.MoneyFlowTimelinePeriod, error) {
	periods := []models.MoneyFlowTimelinePeriod{}
	for rows.Next() {
		var id uuid.UUID
		var start, end time.Time
		var label string
		var income, expense money.Amount
		if err := rows.Scan(&id, &start, &end, &label, &income, &expense); err != nil {
			return nil, err
		}
		incomeAmt := models.NewCurrencyAmounts().Add(accountCurrency, income)
		expenseAmt := models.NewCurrencyAmounts().Add(accountCurrency, expense)
		periods = append(periods, models.MoneyFlowTimelinePeriod{
			Key:       id.String(),
			Label:     label,
			StartDate: start.Format("2006-01-02"),
			EndDate:   end.Format("2006-01-02"),
			Income:    incomeAmt,
			Expense:   expenseAmt,
			Net:       incomeAmt.Sub(expenseAmt),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return periods, nil
}
