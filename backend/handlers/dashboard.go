package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	billingCycleGroupBy  = "billing_cycle"
	defaultBillingCycles = 12
	maxBillingCycles     = 60
)

// GetDashboardSummary aggregates the user's financial overview in one response:
// account and transaction counts, income/expense totals, per-category spend and
// income (top 15 each), a monthly income/expense trend, and the 10 most recent
// transactions. An optional date range and account filter apply to every
// transaction-backed section.
func GetDashboardSummary(c *gin.Context) {
	ctx := c
	userID := auth.GetUserID(c)
	dateFrom := c.Query("dateFrom")
	dateTo := c.Query("dateTo")
	accountID := c.Query("accountId")

	// Billing-cycle view: the whole summary is framed around the statement
	// periods of a single account that has a billing day set.
	if c.Query("groupBy") == billingCycleGroupBy {
		getDashboardSummaryBillingCycle(c)
		return
	}

	var summary models.DashboardSummary

	// Build transaction filters (date range + account). plainFilter is used by
	// queries without a table alias; catFilter prefixes columns with t. for
	// queries that join/alias the transactions table.
	plainFilter := ""
	catFilter := ""
	args := []any{userID}
	paramIdx := 2
	addCond := func(col, op string, val any) {
		plainFilter += fmt.Sprintf(" AND %s %s $%d", col, op, paramIdx)
		catFilter += fmt.Sprintf(" AND t.%s %s $%d", col, op, paramIdx)
		args = append(args, val)
		paramIdx++
	}
	if dateFrom != "" {
		addCond("date", ">=", dateFrom)
	}
	if dateTo != "" {
		addCond("date", "<=", dateTo)
	}
	if accountID != "" {
		addCond("account_id", "=", accountID)
	}

	// Total accounts
	if err := db.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM accounts WHERE user_id = $1", userID).Scan(&summary.TotalAccounts); err != nil {
		slog.Error("GetDashboardSummary (total accounts)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// Total transactions
	if err := db.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM transactions WHERE user_id = $1"+plainFilter, args...).Scan(&summary.TotalTransactions); err != nil {
		slog.Error("GetDashboardSummary (total transactions)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// Income / Expense totals
	incomeQuery := "SELECT COALESCE(SUM(amount), 0) FROM transactions WHERE type = 'credit' AND user_id = $1" + plainFilter
	if err := db.Pool.QueryRow(ctx, incomeQuery, args...).Scan(&summary.TotalIncome); err != nil {
		slog.Error("GetDashboardSummary (total income)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	expenseQuery := "SELECT COALESCE(SUM(amount), 0) FROM transactions WHERE type = 'debit' AND user_id = $1" + plainFilter
	if err := db.Pool.QueryRow(ctx, expenseQuery, args...).Scan(&summary.TotalExpense); err != nil {
		slog.Error("GetDashboardSummary (total expense)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// By category (expenses only)
	catQuery := `SELECT c.id, c.name, c.color, c.icon, COALESCE(SUM(t.amount), 0) as total, COUNT(t.id)
				 FROM categories c
				 LEFT JOIN transactions t ON t.category_id = c.id AND t.type = 'debit' AND t.user_id = $1` + catFilter + `
				 WHERE (c.user_id = $1 OR c.user_id IS NULL)
				 GROUP BY c.id, c.name, c.color, c.icon
				 HAVING COALESCE(SUM(t.amount), 0) > 0
				 ORDER BY total DESC
				 LIMIT 15`

	catRows, err := db.Pool.Query(ctx, catQuery, args...)
	if err != nil {
		slog.Error("GetDashboardSummary (by category)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer catRows.Close()
	for catRows.Next() {
		var cs models.CategorySpend
		if err := catRows.Scan(&cs.CategoryID, &cs.CategoryName, &cs.CategoryColor, &cs.CategoryIcon, &cs.Total, &cs.Count); err != nil {
			slog.Error("GetDashboardSummary scan (by category)", "error", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		summary.ByCategory = append(summary.ByCategory, cs)
	}

	// By category (income only)
	incomeCatQuery := `SELECT c.id, c.name, c.color, c.icon, COALESCE(SUM(t.amount), 0) as total, COUNT(t.id)
				 FROM categories c
				 LEFT JOIN transactions t ON t.category_id = c.id AND t.type = 'credit' AND t.user_id = $1` + catFilter + `
				 WHERE (c.user_id = $1 OR c.user_id IS NULL)
				 GROUP BY c.id, c.name, c.color, c.icon
				 HAVING COALESCE(SUM(t.amount), 0) > 0
				 ORDER BY total DESC
				 LIMIT 15`

	incomeCatRows, err := db.Pool.Query(ctx, incomeCatQuery, args...)
	if err != nil {
		slog.Error("GetDashboardSummary (income by category)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer incomeCatRows.Close()
	for incomeCatRows.Next() {
		var cs models.CategorySpend
		if err := incomeCatRows.Scan(&cs.CategoryID, &cs.CategoryName, &cs.CategoryColor, &cs.CategoryIcon, &cs.Total, &cs.Count); err != nil {
			slog.Error("GetDashboardSummary scan (income by category)", "error", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		summary.IncomeByCategory = append(summary.IncomeByCategory, cs)
	}

	// Monthly trend
	monthlyQuery := `SELECT TO_CHAR(date, 'YYYY-MM') as month,
					 COALESCE(SUM(CASE WHEN type = 'credit' THEN amount ELSE 0 END), 0) as income,
					 COALESCE(SUM(CASE WHEN type = 'debit' THEN amount ELSE 0 END), 0) as expense
					 FROM transactions
					 WHERE user_id = $1` + plainFilter + `
					 GROUP BY TO_CHAR(date, 'YYYY-MM')
					 ORDER BY month`

	monthRows, err := db.Pool.Query(ctx, monthlyQuery, args...)
	if err != nil {
		slog.Error("GetDashboardSummary (monthly trend)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer monthRows.Close()
	for monthRows.Next() {
		var md models.MonthlyData
		if err := monthRows.Scan(&md.Month, &md.Income, &md.Expense); err != nil {
			slog.Error("GetDashboardSummary scan (monthly trend)", "error", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		summary.MonthlyTrend = append(summary.MonthlyTrend, md)
	}

	// Recent transactions
	recentQuery := `SELECT t.id, t.account_id, t.date, t.description, t.amount, t.type,
					t.category_id, t.tags, t.notes, t.payee_id, COALESCE(p.name, '') as payee, t.created_at,
					a.name as account_name,
					COALESCE(c.name, '') as category_name,
					COALESCE(c.icon, '') as category_icon,
					COALESCE(c.color, '') as category_color
					FROM transactions t
					JOIN accounts a ON t.account_id = a.id
					LEFT JOIN categories c ON t.category_id = c.id
					LEFT JOIN payees p ON t.payee_id = p.id
					WHERE t.user_id = $1` + catFilter + `
					ORDER BY t.date DESC, t.created_at DESC
					LIMIT 10`

	recentRows, err := db.Pool.Query(ctx, recentQuery, args...)
	if err != nil {
		slog.Error("GetDashboardSummary (recent transactions)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer recentRows.Close()
	for recentRows.Next() {
		var t models.Transaction
		if err := recentRows.Scan(&t.ID, &t.AccountID, &t.Date, &t.Description, &t.Amount, &t.Type,
			&t.CategoryID, &t.Tags, &t.Notes, &t.PayeeID, &t.Payee, &t.CreatedAt,
			&t.AccountName, &t.CategoryName, &t.CategoryIcon, &t.CategoryColor); err != nil {
			slog.Error("GetDashboardSummary scan (recent transactions)", "error", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		summary.RecentTransactions = append(summary.RecentTransactions, t)
	}

	if summary.ByCategory == nil {
		summary.ByCategory = []models.CategorySpend{}
	}
	if summary.IncomeByCategory == nil {
		summary.IncomeByCategory = []models.CategorySpend{}
	}
	if summary.MonthlyTrend == nil {
		summary.MonthlyTrend = []models.MonthlyData{}
	}
	if summary.RecentTransactions == nil {
		summary.RecentTransactions = []models.Transaction{}
	}

	c.JSON(http.StatusOK, summary)
}

// getDashboardSummaryBillingCycle returns a statement-period framed summary for
// a single account that has a billing day set. The stat-card totals reflect the
// current (in-progress) cycle, the trend is one bar per billing cycle for the
// last `cycles` cycles, the category breakdowns span that cycle window, and the
// recent transactions come from the current cycle. Date-range filters are
// ignored in this mode; the window is defined by billing cycles instead.
func getDashboardSummaryBillingCycle(c *gin.Context) {
	ctx := c
	userID := auth.GetUserID(c)

	accountID, err := uuid.Parse(c.Query("accountId"))
	if err != nil {
		validation.RespondError(c, "accountId is required for billing cycle view", http.StatusBadRequest)
		return
	}

	// The account must exist, belong to the user, and have a billing day set.
	var billingDay *int
	err = db.Pool.QueryRow(ctx,
		`SELECT a.billing_day
		 FROM accounts a WHERE a.id = $1 AND a.user_id = $2`,
		accountID, userID).Scan(&billingDay)
	if errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "account not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("GetDashboardSummary (billing cycle account lookup)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if billingDay == nil {
		validation.RespondError(c, "account has no billing day", http.StatusBadRequest)
		return
	}

	if err := ensureBillingCycles(ctx, db.Pool, userID, accountID, *billingDay); err != nil {
		slog.Error("GetDashboardSummary (ensure billing cycles)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	cycles, err := listBillingCycles(ctx, db.Pool, userID, accountID)
	if err != nil {
		slog.Error("GetDashboardSummary (list billing cycles)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	summary := models.DashboardSummary{
		ByCategory:         []models.CategorySpend{},
		IncomeByCategory:   []models.CategorySpend{},
		MonthlyTrend:       []models.MonthlyData{},
		BillingCycleTrend:  []models.BillingCycleTrendItem{},
		RecentTransactions: []models.Transaction{},
	}

	// Total accounts
	if err := db.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM accounts WHERE user_id = $1", userID).Scan(&summary.TotalAccounts); err != nil {
		slog.Error("GetDashboardSummary (total accounts)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	if len(cycles) == 0 {
		c.JSON(http.StatusOK, summary)
		return
	}

	// Window: the last `cycles` billing cycles, ending with the current one.
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
	current := window[len(window)-1]
	windowStart := window[0].StartDate.Format("2006-01-02")
	windowEnd := window[len(window)-1].EndDate.Format("2006-01-02")

	summary.CurrentCycle = &models.CurrentCycleInfo{
		ID:        current.ID,
		StartDate: current.StartDate,
		EndDate:   current.EndDate,
		Label:     current.Label,
	}

	// Window totals (stat cards): income, expense, and transaction count across
	// every displayed cycle, so the headline numbers agree with the trend chart
	// and category breakdowns.
	cycleStatsQuery := `SELECT COUNT(t.id),
			 COALESCE(SUM(CASE WHEN t.type = 'credit' THEN t.amount ELSE 0 END), 0),
			 COALESCE(SUM(CASE WHEN t.type = 'debit' THEN t.amount ELSE 0 END), 0)
			 FROM transactions t
			 JOIN billing_cycles bc ON t.account_id = bc.account_id AND t.user_id = bc.user_id
			      AND t.date >= bc.start_date AND t.date <= bc.end_date
			 WHERE t.user_id = $1 AND t.account_id = $2
			   AND bc.end_date >= $3 AND bc.end_date <= $4`
	if err := db.Pool.QueryRow(ctx, cycleStatsQuery, userID, accountID, windowStart, windowEnd).
		Scan(&summary.TotalTransactions, &summary.TotalIncome, &summary.TotalExpense); err != nil {
		slog.Error("GetDashboardSummary (cycle window stats)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// Per-cycle trend over the window.
	trendQuery := `SELECT bc.label, bc.start_date, bc.end_date,
			 COALESCE(SUM(CASE WHEN t.type = 'credit' THEN t.amount ELSE 0 END), 0) as income,
			 COALESCE(SUM(CASE WHEN t.type = 'debit' THEN t.amount ELSE 0 END), 0) as expense
			 FROM billing_cycles bc
			 LEFT JOIN transactions t ON t.account_id = bc.account_id AND t.user_id = bc.user_id
			      AND t.date >= bc.start_date AND t.date <= bc.end_date
			 WHERE bc.account_id = $1 AND bc.user_id = $2
			   AND bc.end_date >= $3 AND bc.end_date <= $4
			 GROUP BY bc.id, bc.label, bc.start_date, bc.end_date
			 ORDER BY bc.start_date ASC`
	trendRows, err := db.Pool.Query(ctx, trendQuery, accountID, userID, windowStart, windowEnd)
	if err != nil {
		slog.Error("GetDashboardSummary (billing cycle trend)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer trendRows.Close()
	for trendRows.Next() {
		var item models.BillingCycleTrendItem
		if err := trendRows.Scan(&item.Label, &item.StartDate, &item.EndDate, &item.Income, &item.Expense); err != nil {
			slog.Error("GetDashboardSummary scan (billing cycle trend)", "error", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		summary.BillingCycleTrend = append(summary.BillingCycleTrend, item)
	}

	// Category breakdowns over the cycle window.
	catFilter := ""
	catArgs := []any{userID}
	paramIdx := 2
	addCond := func(col, op string, val any) {
		catFilter += fmt.Sprintf(" AND t.%s %s $%d", col, op, paramIdx)
		catArgs = append(catArgs, val)
		paramIdx++
	}
	addCond("date", ">=", windowStart)
	addCond("date", "<=", windowEnd)
	addCond("account_id", "=", accountID)

	catQuery := `SELECT c.id, c.name, c.color, c.icon, COALESCE(SUM(t.amount), 0) as total, COUNT(t.id)
				 FROM categories c
				 LEFT JOIN transactions t ON t.category_id = c.id AND t.type = 'debit' AND t.user_id = $1` + catFilter + `
				 WHERE (c.user_id = $1 OR c.user_id IS NULL)
				 GROUP BY c.id, c.name, c.color, c.icon
				 HAVING COALESCE(SUM(t.amount), 0) > 0
				 ORDER BY total DESC
				 LIMIT 15`
	catRows, err := db.Pool.Query(ctx, catQuery, catArgs...)
	if err != nil {
		slog.Error("GetDashboardSummary (by category)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer catRows.Close()
	for catRows.Next() {
		var cs models.CategorySpend
		if err := catRows.Scan(&cs.CategoryID, &cs.CategoryName, &cs.CategoryColor, &cs.CategoryIcon, &cs.Total, &cs.Count); err != nil {
			slog.Error("GetDashboardSummary scan (by category)", "error", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		summary.ByCategory = append(summary.ByCategory, cs)
	}

	incomeCatQuery := `SELECT c.id, c.name, c.color, c.icon, COALESCE(SUM(t.amount), 0) as total, COUNT(t.id)
				 FROM categories c
				 LEFT JOIN transactions t ON t.category_id = c.id AND t.type = 'credit' AND t.user_id = $1` + catFilter + `
				 WHERE (c.user_id = $1 OR c.user_id IS NULL)
				 GROUP BY c.id, c.name, c.color, c.icon
				 HAVING COALESCE(SUM(t.amount), 0) > 0
				 ORDER BY total DESC
				 LIMIT 15`
	incomeCatRows, err := db.Pool.Query(ctx, incomeCatQuery, catArgs...)
	if err != nil {
		slog.Error("GetDashboardSummary (income by category)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer incomeCatRows.Close()
	for incomeCatRows.Next() {
		var cs models.CategorySpend
		if err := incomeCatRows.Scan(&cs.CategoryID, &cs.CategoryName, &cs.CategoryColor, &cs.CategoryIcon, &cs.Total, &cs.Count); err != nil {
			slog.Error("GetDashboardSummary scan (income by category)", "error", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		summary.IncomeByCategory = append(summary.IncomeByCategory, cs)
	}

	// Recent transactions across the displayed cycle window (so the list is
	// non-empty even while the current in-progress cycle has no activity yet).
	recentQuery := `SELECT t.id, t.account_id, t.date, t.description, t.amount, t.type,
					t.category_id, t.tags, t.notes, t.payee_id, COALESCE(p.name, '') as payee, t.created_at,
					a.name as account_name,
					COALESCE(c.name, '') as category_name,
					COALESCE(c.icon, '') as category_icon,
					COALESCE(c.color, '') as category_color
					FROM transactions t
					JOIN accounts a ON t.account_id = a.id
					LEFT JOIN categories c ON t.category_id = c.id
					LEFT JOIN payees p ON t.payee_id = p.id
					WHERE t.user_id = $1 AND t.account_id = $2
					  AND t.date >= $3 AND t.date <= $4
					ORDER BY t.date DESC, t.created_at DESC
					LIMIT 10`
	recentRows, err := db.Pool.Query(ctx, recentQuery, userID, accountID, windowStart, windowEnd)
	if err != nil {
		slog.Error("GetDashboardSummary (recent transactions)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer recentRows.Close()
	for recentRows.Next() {
		var t models.Transaction
		if err := recentRows.Scan(&t.ID, &t.AccountID, &t.Date, &t.Description, &t.Amount, &t.Type,
			&t.CategoryID, &t.Tags, &t.Notes, &t.PayeeID, &t.Payee, &t.CreatedAt,
			&t.AccountName, &t.CategoryName, &t.CategoryIcon, &t.CategoryColor); err != nil {
			slog.Error("GetDashboardSummary scan (recent transactions)", "error", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		summary.RecentTransactions = append(summary.RecentTransactions, t)
	}

	c.JSON(http.StatusOK, summary)
}
