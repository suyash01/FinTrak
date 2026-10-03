package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
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

// The GitHub-style cash-flow heatmap: one cell per day, shaded by that day's
// net, over a window the user scrubs.
//
// Two things make this endpoint's arithmetic different from the rest of the
// dashboard, and both are about the heatmap's *scale*:
//
//   - Every cell is per-currency, and so is the scale that shades them. The
//     largest absolute net in the window is `MaxAbsNet`, which is itself a
//     CurrencyAmounts map for the same reason the cells are: adding across
//     currencies to get one "biggest day" would be meaningless. It is the
//     standing example of a value that could only exist by adding across
//     currencies and is therefore a map, not a number.
//   - A day's Net is a difference *inside* one currency, computed with Sub — not
//     two independently-derived figures subtracted by the client.
//
// The overlay fields are the other half. When a single account is selected, the
// response carries that account's billing-cycle boundaries and the same
// synthetic summary rows the transactions list shows (see transaction_summary.go)
// so the calendar can draw them in place. Note that `cycles[].outstanding` and
// `markers[].amount` are keyed by the named account's own currency and are NOT
// narrowed by `?currency=`, because attachCashFlowOverlays takes no currency
// argument. A filter can therefore still be answered with a figure in a currency
// it excluded; that is deliberate and documented rather than an oversight.

// GetCashFlowCalendar aggregates the user's transactions by day for the
// GitHub-style cash-flow heatmap: per-day income, expense, and net over an
// optional date range and account filter. When a single account is supplied,
// the response also carries that account's billing-cycle boundaries and the
// synthetic summary rows already shown in the transactions list (month-end
// running balances, or per-cycle total outstanding when the account has a
// billing day) so they can be overlaid on the calendar.
//
// Every amount is per-currency, including the heatmap's own scale: with no
// account filter the window can span an INR account and a USD one, and neither a
// day's net nor the denominator that renders it can be a single number. The
// response carries one amount per currency alongside the scope that produced it.
func (srv *Server) GetCashFlowCalendar(c *gin.Context) {
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

	var accountUUID uuid.UUID
	if accountID != "" {
		parsed, err := uuid.Parse(accountID)
		if err != nil {
			validation.RespondError(c, "invalid accountId", http.StatusBadRequest)
			return
		}
		accountUUID = parsed
	}
	// The currency is parsed once here and passed to both reads below, so the
	// scope and the days it is the sum of narrow on the same normalised code.
	currency, ok := parseCurrency(c)
	if !ok {
		return
	}

	// The scope query names the currencies the days below are reported in, and
	// it also supplies the window totals. It reads accounts rather than
	// transactions, so an account quiet in the window still appears and explains
	// the currency it holds.
	//
	// Both reads run in one read-only, repeatable-read transaction. The totals are
	// the days' own explanation of themselves, so a write landing between two
	// autocommit statements would let them describe different ledgers - a day's
	// amounts and the totals beside it disagreeing, which is the silent gap this
	// response exists to close. Same argument, and same shape, as the dashboard
	// summary, the money-flow graph and the money-flow timeline.
	tx, err := srv.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		slog.Error("GetCashFlowCalendar (begin)", slog.String("error", err.Error()))
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
		slog.Error("GetCashFlowCalendar (currency scope)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	days, maxAbsNet, err := queryDailyCashFlow(ctx, tx, userID, dateFrom, dateTo, accountID, currency)
	if err != nil {
		slog.Error("GetCashFlowCalendar (daily flow)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		slog.Error("GetCashFlowCalendar (commit)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// The window totals come from the scope rather than from summing the day rows
	// above, so the calendar's and the dashboard's are computed by the same
	// query. Net is the server's per-currency difference: income minus expense is
	// only defined inside one currency, and only the server knows which currencies
	// are in scope.
	calendar := models.CashFlowCalendar{
		Days:          days,
		Markers:       []models.CashFlowCalendarMarker{},
		Cycles:        []models.CashFlowCalendarCycle{},
		TotalIncome:   scope.Income,
		TotalExpense:  scope.Expense,
		Net:           scope.Net(),
		MaxAbsNet:     maxAbsNet,
		CurrencyScope: scope.Scope,
	}

	// The overlays are best-effort and read outside the snapshot above: with a
	// billing day set the overlay regenerates the account's cycles, which writes,
	// so it cannot share a read-only transaction. They are one account's own
	// figures rather than a window total, so nothing in the days or the totals
	// depends on them agreeing.
	if accountID != "" {
		srv.attachCashFlowOverlays(ctx, userID, accountUUID, dateFrom, dateTo, &calendar)
	}

	c.JSON(http.StatusOK, calendar)
}

// attachCashFlowOverlays adds billing-cycle boundaries and synthetic summary
// markers for a single account. Overlays are best-effort: the daily flow is the
// page's primary content, so a failure here logs and leaves the calendar
// without overlays rather than failing the whole request.
//
// The account's currency is read here, beside its billing day, because every
// amount the overlays carry is that one account's money: the marker and the cycle
// outstanding are per-currency maps holding this account's single key, so a
// client never has to learn a second shape for the same field. Reading it in the
// query already being issued is what keeps the overlays from costing a statement
// of their own - and it is read through the same expression every other
// currency read in this file uses.
func (srv *Server) attachCashFlowOverlays(c *gin.Context, userID, accountID uuid.UUID, dateFrom, dateTo string, calendar *models.CashFlowCalendar) {
	var (
		billingDay *int
		currency   string
	)
	err := srv.db.QueryRow(c,
		`SELECT a.billing_day, `+flowCurrency("a.currency")+`
		 FROM accounts a WHERE a.id = $1 AND a.user_id = $2`,
		accountID, userID).Scan(&billingDay, &currency)
	if errors.Is(err, pgx.ErrNoRows) {
		// Unknown account (or one the user does not own): no overlays.
		return
	}
	if err != nil {
		slog.Error("attachCashFlowOverlays (account lookup)", slog.String("error", err.Error()))
		return
	}

	if billingDay == nil {
		rows := srv.computeMonthEndBalanceRows(c, userID, accountID, "", dateFrom, dateTo)
		calendar.Markers = cashFlowMarkers(rows, "balance", currency)
		return
	}

	// The regeneration detaches and recreates the account's cycles, so it runs
	// in one transaction: an interruption between the detach and the delete
	// would otherwise leave its transactions detached from cycles that still
	// exist.
	if err := db.WithTx(c, srv.db, func(tx pgx.Tx) error {
		return ensureBillingCycles(c, tx, userID, accountID, *billingDay)
	}); err != nil {
		slog.Error("attachCashFlowOverlays (ensure billing cycles)", slog.String("error", err.Error()))
		return
	}

	cycles, err := listBillingCycles(c, srv.db, userID, accountID)
	if err != nil {
		slog.Error("attachCashFlowOverlays (list billing cycles)", slog.String("error", err.Error()))
		return
	}
	calendar.Cycles = make([]models.CashFlowCalendarCycle, 0, len(cycles))
	for _, bc := range cycles {
		calendar.Cycles = append(calendar.Cycles, models.CashFlowCalendarCycle{
			ID:        bc.ID,
			Label:     bc.Label,
			StartDate: bc.StartDate,
			EndDate:   bc.EndDate,
			// A cycle belongs to exactly one account, so its running balance is
			// that account's own currency - one key, never a sum of two.
			Outstanding: models.NewCurrencyAmounts().Add(currency, bc.TotalOutstanding),
		})
	}

	rows := srv.summaryRowsFromCycles(c, userID, accountID, "", dateFrom, dateTo, cycles)
	calendar.Markers = cashFlowMarkers(rows, "outstanding", currency)
}

// cashFlowMarkers converts the synthetic summary rows returned by the existing
// month-end/cycle helpers into the lighter calendar marker shape. currency is the
// selected account's own: every row is a figure for that one account, so each
// marker holds a single key.
func cashFlowMarkers(rows []models.Transaction, kind, currency string) []models.CashFlowCalendarMarker {
	markers := make([]models.CashFlowCalendarMarker, 0, len(rows))
	for _, r := range rows {
		markers = append(markers, models.CashFlowCalendarMarker{
			Date:   r.Date.Format("2006-01-02"),
			Label:  r.Description,
			Kind:   kind,
			Amount: models.NewCurrencyAmounts().Add(currency, r.Amount),
		})
	}
	return markers
}

// queryDailyCashFlow groups the filtered transactions by day, returning the
// per-day amounts plus the largest absolute daily net per currency (for
// client-side heatmap scaling).
//
// The account's currency is projected and grouped with the date, so one day is
// one entry holding one amount per currency rather than one entry per currency.
// Grouping the day without the currency is what added dollars to rupees: the
// database hands back a single amount that already no longer means anything, and
// no map later in the response can undo it. The reading of a transaction's
// denomination off its account rests on a transaction having no currency of its
// own; see the same premise in money_flow_timeline.go.
//
// maxAbsNet is per currency because a single scale across currencies is
// meaningless - a quiet foreign account's real deficit would render as a flat
// cell beside a large domestic one. The client picks its own currency's
// denominator; nothing here compares one currency's amount with another's.
func queryDailyCashFlow(ctx context.Context, db flowQueryer, userID uuid.UUID, dateFrom, dateTo, accountID, currency string) ([]models.CashFlowCalendarDay, models.CurrencyAmounts, error) {
	cond, args, _ := flowFilter("t", "a", 2, dateFrom, dateTo, accountID, currency)
	filter := ""
	if cond != "" {
		filter = " AND " + cond
	}

	rows, err := db.Query(ctx, `
		SELECT t.date,
			   `+flowCurrency("a.currency")+` AS currency,
			   COALESCE(SUM(CASE WHEN t.type = 'credit' THEN t.amount ELSE 0 END), 0),
			   COALESCE(SUM(CASE WHEN t.type = 'debit' THEN t.amount ELSE 0 END), 0),
			   COUNT(*)
		FROM transactions t
		JOIN accounts a ON t.account_id = a.id
		WHERE t.user_id = $1`+filter+`
		GROUP BY t.date, `+flowCurrency("a.currency")+`
		ORDER BY t.date`, append([]any{userID}, args...)...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	// The rows arrive in date order, one per date per currency, so the fold keeps
	// the dates it has seen in the order it saw them. Each entry starts from
	// NewCurrencyAmounts rather than nil, because a nil map marshals as null
	// where the contract promises {}.
	byDate := map[string]*models.CashFlowCalendarDay{}
	var order []string
	for rows.Next() {
		var date time.Time
		var code string
		var income, expense money.Amount
		var count int
		if err := rows.Scan(&date, &code, &income, &expense, &count); err != nil {
			return nil, nil, err
		}
		key := date.Format("2006-01-02")
		entry, ok := byDate[key]
		if !ok {
			entry = &models.CashFlowCalendarDay{
				Date:    key,
				Income:  models.NewCurrencyAmounts(),
				Expense: models.NewCurrencyAmounts(),
				Net:     models.NewCurrencyAmounts(),
			}
			byDate[key] = entry
			order = append(order, key)
		}
		entry.Income = entry.Income.Add(code, income)
		entry.Expense = entry.Expense.Add(code, expense)
		entry.Count += count
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	days := make([]models.CashFlowCalendarDay, 0, len(order))
	maxAbsNet := models.NewCurrencyAmounts()
	for _, key := range order {
		entry := byDate[key]
		// Net is the difference inside one currency, and the server takes it: a
		// client subtracting two currency-keyed objects by hand is how a number
		// across two currencies comes back. Sub takes the union of the keys and
		// reads a missing one as zero, so a day that only spent still reports its
		// negative net under the currency it spent in.
		entry.Net = entry.Income.Sub(entry.Expense)
		days = append(days, *entry)
	}
	// The heatmap scale is the largest |net| each currency reaches, not the
	// largest in the window: comparing 30000 cents of dollars with 5000000
	// cents of rupees is a comparison of two numbers in different units, and the
	// cell it flattens is the foreign account's real deficit.
	for _, day := range days {
		for code, net := range day.Net {
			if abs := net.Abs(); abs > maxAbsNet[code] {
				maxAbsNet[code] = abs
			}
		}
	}
	return days, maxAbsNet, nil
}
