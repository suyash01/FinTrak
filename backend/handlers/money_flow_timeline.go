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
	scope, err := srv.currencyScope(ctx, srv.db, userID, scopeOptions{
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

	periods, err := queryMonthlyFlowTimeline(ctx, srv.db, userID, dateFrom, dateTo, accountID, currency)
	if err != nil {
		slog.Error("GetMoneyFlowTimeline (monthly)", slog.String("error", err.Error()))
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

	// The account's currency is read once here rather than per period: a cycle
	// belongs to exactly one account, and the API only lets a transaction be
	// assigned to a cycle of its own account, so every period of this timeline is
	// in that one currency. The maps below are per-currency for consistency with
	// the monthly view, not because this window can span currencies.
	var billingDay *int
	var accountCurrency string
	err = srv.db.QueryRow(ctx,
		`SELECT a.billing_day, `+flowCurrency("a.currency")+`
		 FROM accounts a WHERE a.id = $1 AND a.user_id = $2`,
		accountID, userID).Scan(&billingDay, &accountCurrency)
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

	scope, err := srv.currencyScope(ctx, srv.db, userID, scopeOptions{
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

	rows, err := srv.db.Query(ctx, `
		SELECT bc.id, bc.start_date, bc.end_date, bc.label,
			   COALESCE(SUM(CASE WHEN t.type = 'credit' THEN t.amount ELSE 0 END), 0),
			   COALESCE(SUM(CASE WHEN t.type = 'debit' THEN t.amount ELSE 0 END), 0)
		FROM billing_cycles bc
		LEFT JOIN transactions t ON t.billing_cycle_id = bc.id
		JOIN accounts a ON a.id = bc.account_id
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

	periods := []models.MoneyFlowTimelinePeriod{}
	for rows.Next() {
		var id uuid.UUID
		var start, end time.Time
		var label string
		var income, expense money.Amount
		if err := rows.Scan(&id, &start, &end, &label, &income, &expense); err != nil {
			slog.Error("GetMoneyFlowTimeline scan (cycle periods)", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
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

	c.JSON(http.StatusOK, models.MoneyFlowTimeline{
		GroupBy:       billingCycleGroupBy,
		Periods:       periods,
		CurrencyScope: scope.Scope,
	})
}
