package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/internal/money"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// GetCashFlowCalendar aggregates the user's transactions by day for the
// GitHub-style cash-flow heatmap: per-day income, expense, and net over an
// optional date range and account filter. When a single account is supplied,
// the response also carries that account's billing-cycle boundaries and the
// synthetic summary rows already shown in the transactions list (month-end
// running balances, or per-cycle total outstanding when the account has a
// billing day) so they can be overlaid on the calendar.
func (srv *Server) GetCashFlowCalendar(c *gin.Context) {
	ctx := c
	userID := auth.GetUserID(c)
	dateFrom := c.Query("dateFrom")
	dateTo := c.Query("dateTo")
	accountID := c.Query("accountId")

	// The account id is compared against a uuid column, so reject a malformed
	// value up front instead of letting it surface as a 500.
	var accountUUID uuid.UUID
	if accountID != "" {
		parsed, err := uuid.Parse(accountID)
		if err != nil {
			validation.RespondError(c, "invalid accountId", http.StatusBadRequest)
			return
		}
		accountUUID = parsed
	}

	tx, err := srv.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		slog.Error("GetCashFlowCalendar (begin)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(ctx)

	days, totalIncome, totalExpense, maxAbsNet, err := queryDailyCashFlow(ctx, tx, userID, dateFrom, dateTo, accountID)
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

	calendar := models.CashFlowCalendar{
		Days:         days,
		Markers:      []models.CashFlowCalendarMarker{},
		Cycles:       []models.CashFlowCalendarCycle{},
		TotalIncome:  totalIncome,
		TotalExpense: totalExpense,
		Net:          totalIncome - totalExpense,
		MaxAbsNet:    maxAbsNet,
	}

	if accountID != "" {
		srv.attachCashFlowOverlays(ctx, userID, accountUUID, dateFrom, dateTo, &calendar)
	}

	c.JSON(http.StatusOK, calendar)
}

// attachCashFlowOverlays adds billing-cycle boundaries and synthetic summary
// markers for a single account. Overlays are best-effort: the daily flow is the
// page's primary content, so a failure here logs and leaves the calendar
// without overlays rather than failing the whole request.
func (srv *Server) attachCashFlowOverlays(c *gin.Context, userID, accountID uuid.UUID, dateFrom, dateTo string, calendar *models.CashFlowCalendar) {
	var billingDay *int
	err := srv.db.QueryRow(c,
		`SELECT a.billing_day
		 FROM accounts a WHERE a.id = $1 AND a.user_id = $2`,
		accountID, userID).Scan(&billingDay)
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
		calendar.Markers = cashFlowMarkers(rows, "balance")
		return
	}

	if err := ensureBillingCycles(c, srv.db, userID, accountID, *billingDay); err != nil {
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
			ID:          bc.ID,
			Label:       bc.Label,
			StartDate:   bc.StartDate,
			EndDate:     bc.EndDate,
			Outstanding: bc.TotalOutstanding,
		})
	}

	rows := srv.summaryRowsFromCycles(c, userID, accountID, "", dateFrom, dateTo, cycles)
	calendar.Markers = cashFlowMarkers(rows, "outstanding")
}

// cashFlowMarkers converts the synthetic summary rows returned by the existing
// month-end/cycle helpers into the lighter calendar marker shape.
func cashFlowMarkers(rows []models.Transaction, kind string) []models.CashFlowCalendarMarker {
	markers := make([]models.CashFlowCalendarMarker, 0, len(rows))
	for _, r := range rows {
		markers = append(markers, models.CashFlowCalendarMarker{
			Date:   r.Date.Format("2006-01-02"),
			Label:  r.Description,
			Kind:   kind,
			Amount: r.Amount,
		})
	}
	return markers
}

// queryDailyCashFlow groups the filtered transactions by day, returning the
// per-day aggregates plus the window totals and the largest absolute daily net
// (for client-side heatmap scaling).
func queryDailyCashFlow(ctx context.Context, db flowQueryer, userID uuid.UUID, dateFrom, dateTo, accountID string) ([]models.CashFlowCalendarDay, money.Amount, money.Amount, money.Amount, error) {
	cond, args, _ := flowFilter("t", "a", 2, dateFrom, dateTo, accountID)
	filter := ""
	if cond != "" {
		filter = " AND " + cond
	}

	rows, err := db.Query(ctx, `
		SELECT t.date,
			   COALESCE(SUM(CASE WHEN t.type = 'credit' THEN t.amount ELSE 0 END), 0),
			   COALESCE(SUM(CASE WHEN t.type = 'debit' THEN t.amount ELSE 0 END), 0),
			   COUNT(*)
		FROM transactions t
		JOIN accounts a ON t.account_id = a.id
		WHERE t.user_id = $1`+filter+`
		GROUP BY t.date
		ORDER BY t.date`, append([]any{userID}, args...)...)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	defer rows.Close()

	days := []models.CashFlowCalendarDay{}
	var totalIncome, totalExpense, maxAbsNet money.Amount
	for rows.Next() {
		var date time.Time
		var income, expense money.Amount
		var count int
		if err := rows.Scan(&date, &income, &expense, &count); err != nil {
			return nil, 0, 0, 0, err
		}
		net := income - expense
		days = append(days, models.CashFlowCalendarDay{
			Date:    date.Format("2006-01-02"),
			Income:  income,
			Expense: expense,
			Net:     net,
			Count:   count,
		})
		totalIncome += income
		totalExpense += expense
		absNet := net
		if absNet < 0 {
			absNet = -absNet
		}
		if absNet > maxAbsNet {
			maxAbsNet = absNet
		}
	}
	return days, totalIncome, totalExpense, maxAbsNet, rows.Err()
}
