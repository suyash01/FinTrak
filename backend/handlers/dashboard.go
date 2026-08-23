package handlers

import (
	"fmt"
	"log"
	"net/http"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
)

func GetDashboardSummary(c *gin.Context) {
	ctx := c
	userID := auth.GetUserID(c)
	dateFrom := c.Query("dateFrom")
	dateTo := c.Query("dateTo")
	accountID := c.Query("accountId")

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
		log.Printf("Error in GetDashboardSummary (total accounts): %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// Total transactions
	if err := db.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM transactions WHERE user_id = $1"+plainFilter, args...).Scan(&summary.TotalTransactions); err != nil {
		log.Printf("Error in GetDashboardSummary (total transactions): %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// Income / Expense totals
	incomeQuery := "SELECT COALESCE(SUM(amount), 0) FROM transactions WHERE type = 'credit' AND user_id = $1" + plainFilter
	if err := db.Pool.QueryRow(ctx, incomeQuery, args...).Scan(&summary.TotalIncome); err != nil {
		log.Printf("Error in GetDashboardSummary (total income): %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	expenseQuery := "SELECT COALESCE(SUM(amount), 0) FROM transactions WHERE type = 'debit' AND user_id = $1" + plainFilter
	if err := db.Pool.QueryRow(ctx, expenseQuery, args...).Scan(&summary.TotalExpense); err != nil {
		log.Printf("Error in GetDashboardSummary (total expense): %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// By category (expenses only)
	catQuery := `SELECT c.id, c.name, c.color, c.icon, COALESCE(SUM(t.amount), 0) as total, COUNT(t.id)
				 FROM categories c
				 LEFT JOIN transactions t ON t.category_id = c.id AND t.type = 'debit' AND t.user_id = $1` + catFilter + `
				 WHERE c.user_id = $1
				 GROUP BY c.id, c.name, c.color, c.icon
				 HAVING COALESCE(SUM(t.amount), 0) > 0
				 ORDER BY total DESC
				 LIMIT 15`

	catRows, err := db.Pool.Query(ctx, catQuery, args...)
	if err != nil {
		log.Printf("Error in GetDashboardSummary (by category): %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer catRows.Close()
	for catRows.Next() {
		var cs models.CategorySpend
		if err := catRows.Scan(&cs.CategoryID, &cs.CategoryName, &cs.CategoryColor, &cs.CategoryIcon, &cs.Total, &cs.Count); err != nil {
			log.Printf("Error in GetDashboardSummary scan (by category): %v\n", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		summary.ByCategory = append(summary.ByCategory, cs)
	}

	// By category (income only)
	incomeCatQuery := `SELECT c.id, c.name, c.color, c.icon, COALESCE(SUM(t.amount), 0) as total, COUNT(t.id)
				 FROM categories c
				 LEFT JOIN transactions t ON t.category_id = c.id AND t.type = 'credit' AND t.user_id = $1` + catFilter + `
				 WHERE c.user_id = $1
				 GROUP BY c.id, c.name, c.color, c.icon
				 HAVING COALESCE(SUM(t.amount), 0) > 0
				 ORDER BY total DESC
				 LIMIT 15`

	incomeCatRows, err := db.Pool.Query(ctx, incomeCatQuery, args...)
	if err != nil {
		log.Printf("Error in GetDashboardSummary (income by category): %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer incomeCatRows.Close()
	for incomeCatRows.Next() {
		var cs models.CategorySpend
		if err := incomeCatRows.Scan(&cs.CategoryID, &cs.CategoryName, &cs.CategoryColor, &cs.CategoryIcon, &cs.Total, &cs.Count); err != nil {
			log.Printf("Error in GetDashboardSummary scan (income by category): %v\n", err)
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
		log.Printf("Error in GetDashboardSummary (monthly trend): %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer monthRows.Close()
	for monthRows.Next() {
		var md models.MonthlyData
		if err := monthRows.Scan(&md.Month, &md.Income, &md.Expense); err != nil {
			log.Printf("Error in GetDashboardSummary scan (monthly trend): %v\n", err)
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
		log.Printf("Error in GetDashboardSummary (recent transactions): %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer recentRows.Close()
	for recentRows.Next() {
		var t models.Transaction
		if err := recentRows.Scan(&t.ID, &t.AccountID, &t.Date, &t.Description, &t.Amount, &t.Type,
			&t.CategoryID, &t.Tags, &t.Notes, &t.PayeeID, &t.Payee, &t.CreatedAt,
			&t.AccountName, &t.CategoryName, &t.CategoryIcon, &t.CategoryColor); err != nil {
			log.Printf("Error in GetDashboardSummary scan (recent transactions): %v\n", err)
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
