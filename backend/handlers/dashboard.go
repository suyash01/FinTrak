package handlers

import (
	"fmt"
	"log"
	"net/http"

	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
)

func GetDashboardSummary(c *gin.Context) {
	ctx := c
	dateFrom := c.Query("dateFrom")
	dateTo := c.Query("dateTo")

	var summary models.DashboardSummary

	// Total accounts
	if err := db.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM accounts").Scan(&summary.TotalAccounts); err != nil {
		log.Printf("Error in GetDashboardSummary (total accounts): %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	// Total transactions
	if err := db.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM transactions").Scan(&summary.TotalTransactions); err != nil {
		log.Printf("Error in GetDashboardSummary (total transactions): %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	// Income / Expense totals
	dateFilter := ""
	args := []any{}
	paramIdx := 1
	if dateFrom != "" {
		dateFilter += fmt.Sprintf(" AND date >= $%d", paramIdx)
		args = append(args, dateFrom)
		paramIdx++
	}
	if dateTo != "" {
		dateFilter += fmt.Sprintf(" AND date <= $%d", paramIdx)
		args = append(args, dateTo)
		paramIdx++
	}

	incomeQuery := "SELECT COALESCE(SUM(amount), 0) FROM transactions WHERE type = 'credit'" + dateFilter
	if err := db.Pool.QueryRow(ctx, incomeQuery, args...).Scan(&summary.TotalIncome); err != nil {
		log.Printf("Error in GetDashboardSummary (total income): %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	expenseQuery := "SELECT COALESCE(SUM(amount), 0) FROM transactions WHERE type = 'debit'" + dateFilter
	if err := db.Pool.QueryRow(ctx, expenseQuery, args...).Scan(&summary.TotalExpense); err != nil {
		log.Printf("Error in GetDashboardSummary (total expense): %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	// By category (expenses only)
	catQuery := `SELECT c.id, c.name, c.color, c.icon, COALESCE(SUM(t.amount), 0) as total, COUNT(t.id)
				 FROM categories c
				 LEFT JOIN transactions t ON t.category_id = c.id AND t.type = 'debit'` + dateFilter + `
				 GROUP BY c.id, c.name, c.color, c.icon
				 HAVING COALESCE(SUM(t.amount), 0) > 0
				 ORDER BY total DESC
				 LIMIT 15`

	catRows, err := db.Pool.Query(ctx, catQuery, args...)
	if err != nil {
		log.Printf("Error in GetDashboardSummary (by category): %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	defer catRows.Close()
	for catRows.Next() {
		var cs models.CategorySpend
		if err := catRows.Scan(&cs.CategoryID, &cs.CategoryName, &cs.CategoryColor, &cs.CategoryIcon, &cs.Total, &cs.Count); err != nil {
			log.Printf("Error in GetDashboardSummary scan (by category): %v\n", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
			return
		}
		summary.ByCategory = append(summary.ByCategory, cs)
	}

	// By category (income only)
	incomeCatQuery := `SELECT c.id, c.name, c.color, c.icon, COALESCE(SUM(t.amount), 0) as total, COUNT(t.id)
				 FROM categories c
				 LEFT JOIN transactions t ON t.category_id = c.id AND t.type = 'credit'` + dateFilter + `
				 GROUP BY c.id, c.name, c.color, c.icon
				 HAVING COALESCE(SUM(t.amount), 0) > 0
				 ORDER BY total DESC
				 LIMIT 15`

	incomeCatRows, err := db.Pool.Query(ctx, incomeCatQuery, args...)
	if err != nil {
		log.Printf("Error in GetDashboardSummary (income by category): %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	defer incomeCatRows.Close()
	for incomeCatRows.Next() {
		var cs models.CategorySpend
		if err := incomeCatRows.Scan(&cs.CategoryID, &cs.CategoryName, &cs.CategoryColor, &cs.CategoryIcon, &cs.Total, &cs.Count); err != nil {
			log.Printf("Error in GetDashboardSummary scan (income by category): %v\n", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
			return
		}
		summary.IncomeByCategory = append(summary.IncomeByCategory, cs)
	}

	// Monthly trend (last 12 months)
	monthlyQuery := `SELECT TO_CHAR(date, 'YYYY-MM') as month,
					 COALESCE(SUM(CASE WHEN type = 'credit' THEN amount ELSE 0 END), 0) as income,
					 COALESCE(SUM(CASE WHEN type = 'debit' THEN amount ELSE 0 END), 0) as expense
					 FROM transactions
					 WHERE date >= NOW() - INTERVAL '12 months'
					 GROUP BY TO_CHAR(date, 'YYYY-MM')
					 ORDER BY month`

	monthRows, err := db.Pool.Query(ctx, monthlyQuery)
	if err != nil {
		log.Printf("Error in GetDashboardSummary (monthly trend): %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	defer monthRows.Close()
	for monthRows.Next() {
		var md models.MonthlyData
		if err := monthRows.Scan(&md.Month, &md.Income, &md.Expense); err != nil {
			log.Printf("Error in GetDashboardSummary scan (monthly trend): %v\n", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
			return
		}
		summary.MonthlyTrend = append(summary.MonthlyTrend, md)
	}

	// Recent transactions
	recentQuery := `SELECT t.id, t.account_id, t.date, t.description, t.amount, t.type,
					t.category_id, t.tags, t.notes, t.payee, COALESCE(t.hash, ''), t.created_at,
					a.name as account_name,
					COALESCE(c.name, '') as category_name,
					COALESCE(c.icon, '') as category_icon,
					COALESCE(c.color, '') as category_color
					FROM transactions t
					JOIN accounts a ON t.account_id = a.id
					LEFT JOIN categories c ON t.category_id = c.id
					ORDER BY t.date DESC, t.created_at DESC
					LIMIT 10`

	recentRows, err := db.Pool.Query(ctx, recentQuery)
	if err != nil {
		log.Printf("Error in GetDashboardSummary (recent transactions): %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	defer recentRows.Close()
	for recentRows.Next() {
		var t models.Transaction
		if err := recentRows.Scan(&t.ID, &t.AccountID, &t.Date, &t.Description, &t.Amount, &t.Type,
			&t.CategoryID, &t.Tags, &t.Notes, &t.Payee, &t.Hash, &t.CreatedAt,
			&t.AccountName, &t.CategoryName, &t.CategoryIcon, &t.CategoryColor); err != nil {
			log.Printf("Error in GetDashboardSummary scan (recent transactions): %v\n", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
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
