package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/fintrak/backend/auth"
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

	if groupBy == billingCycleGroupBy {
		srv.getMoneyFlowTimelineBillingCycle(c)
		return
	}

	periods, err := queryMonthlyFlowTimeline(ctx, srv.db, userID, dateFrom, dateTo, accountID)
	if err != nil {
		slog.Error("GetMoneyFlowTimeline (monthly)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, models.MoneyFlowTimeline{GroupBy: "month", Periods: periods})
}

// queryMonthlyFlowTimeline groups the filtered transactions into calendar
// months, returning the inclusive month bounds the client re-queries with.
func queryMonthlyFlowTimeline(ctx context.Context, db cycleQueryer, userID uuid.UUID, dateFrom, dateTo, accountID string) ([]models.MoneyFlowTimelinePeriod, error) {
	cond, args, _ := flowFilter("t", "a", 2, dateFrom, dateTo, accountID)
	filter := ""
	if cond != "" {
		filter = " AND " + cond
	}

	rows, err := db.Query(ctx, `
		SELECT date_trunc('month', t.date)::date,
			   COALESCE(SUM(CASE WHEN t.type = 'credit' THEN t.amount ELSE 0 END), 0),
			   COALESCE(SUM(CASE WHEN t.type = 'debit' THEN t.amount ELSE 0 END), 0)
		FROM transactions t
		JOIN accounts a ON t.account_id = a.id
		WHERE t.user_id = $1`+filter+`
		GROUP BY 1
		ORDER BY 1`, append([]any{userID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	periods := []models.MoneyFlowTimelinePeriod{}
	for rows.Next() {
		var start time.Time
		var income, expense money.Amount
		if err := rows.Scan(&start, &income, &expense); err != nil {
			return nil, err
		}
		start = time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC)
		end := time.Date(start.Year(), start.Month()+1, 0, 0, 0, 0, 0, time.UTC)
		periods = append(periods, models.MoneyFlowTimelinePeriod{
			Key:       start.Format("2006-01"),
			Label:     start.Format("Jan 2006"),
			StartDate: start.Format("2006-01-02"),
			EndDate:   end.Format("2006-01-02"),
			Income:    income,
			Expense:   expense,
			Net:       income - expense,
		})
	}
	return periods, rows.Err()
}

// getMoneyFlowTimelineBillingCycle frames the timeline around the statement
// periods of one billing-day account, mirroring the dashboard's billing-cycle
// window: the last `cycles` cycles ending with the current one.
func (srv *Server) getMoneyFlowTimelineBillingCycle(c *gin.Context) {
	ctx := c
	userID := auth.GetUserID(c)

	accountID, err := uuid.Parse(c.Query("accountId"))
	if err != nil {
		validation.RespondError(c, "accountId is required for billing cycle view", http.StatusBadRequest)
		return
	}

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

	if err := ensureBillingCycles(ctx, srv.db, userID, accountID, *billingDay); err != nil {
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
		c.JSON(http.StatusOK, models.MoneyFlowTimeline{GroupBy: billingCycleGroupBy, Periods: []models.MoneyFlowTimelinePeriod{}})
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

	rows, err := srv.db.Query(ctx, `
		SELECT bc.id, bc.start_date, bc.end_date, bc.label,
			   COALESCE(SUM(CASE WHEN t.type = 'credit' THEN t.amount ELSE 0 END), 0),
			   COALESCE(SUM(CASE WHEN t.type = 'debit' THEN t.amount ELSE 0 END), 0)
		FROM billing_cycles bc
		LEFT JOIN transactions t ON t.account_id = bc.account_id AND t.user_id = bc.user_id
		     AND t.date >= bc.start_date AND t.date <= bc.end_date
		WHERE bc.account_id = $1 AND bc.user_id = $2
		  AND bc.end_date >= $3 AND bc.end_date <= $4
		GROUP BY bc.id, bc.start_date, bc.end_date, bc.label
		ORDER BY bc.start_date ASC`,
		accountID, userID, windowStart, windowEnd)
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
		periods = append(periods, models.MoneyFlowTimelinePeriod{
			Key:       id.String(),
			Label:     label,
			StartDate: start.Format("2006-01-02"),
			EndDate:   end.Format("2006-01-02"),
			Income:    income,
			Expense:   expense,
			Net:       income - expense,
		})
	}

	c.JSON(http.StatusOK, models.MoneyFlowTimeline{GroupBy: billingCycleGroupBy, Periods: periods})
}
