package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v3"
	"github.com/stretchr/testify/assert"
)

func newDashboardTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.GET("/dashboard/summary", GetDashboardSummary)
	return r
}

func TestGetDashboardSummary(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	r := newDashboardTestRouter()
	userID := testUserID()
	now := time.Now()

	catID := uuid.New()
	accountID := uuid.New()
	txnID := uuid.New()

	// 1. Total accounts
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))

	// 2. Total transactions
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(120))

	// 3. Total income
	mock.ExpectQuery("SELECT COALESCE\\(SUM\\(amount\\), 0\\) FROM transactions WHERE type = 'credit'").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"sum"}).AddRow(50000.00))

	// 4. Total expense
	mock.ExpectQuery("SELECT COALESCE\\(SUM\\(amount\\), 0\\) FROM transactions WHERE type = 'debit'").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"sum"}).AddRow(30000.50))

	// 5. Expense by category
	mock.ExpectQuery("t.type = 'debit' AND t.user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "total", "count"}).
			AddRow(catID, "Food", "#f97316", "utensils", 12000.00, 8))

	// 6. Income by category
	mock.ExpectQuery("t.type = 'credit' AND t.user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "total", "count"}).
			AddRow(catID, "Salary", "#22c55e", "wallet", 50000.00, 1))

	// 7. Monthly trend
	mock.ExpectQuery("SELECT TO_CHAR\\(date, 'YYYY-MM'\\) as month").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"month", "income", "expense"}).
			AddRow("2026-07", 50000.00, 30000.50))

	// 8. Recent transactions
	mock.ExpectQuery("SELECT t.id, t.account_id, t.date, t.description, t.amount, t.type").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "account_id", "date", "description", "amount", "type",
			"category_id", "tags", "notes", "payee_id", "payee", "created_at",
			"account_name", "category_name", "category_icon", "category_color",
		}).
			AddRow(txnID, accountID, now, "Zomato order", 450.00, "debit",
				nil, []string{"food"}, "", nil, "Zomato", now,
				"Savings", "Food", "utensils", "#f97316"))

	req, _ := http.NewRequest(http.MethodGet, "/dashboard/summary", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var summary models.DashboardSummary
	err = json.Unmarshal(w.Body.Bytes(), &summary)
	assert.NoError(t, err)

	assert.Equal(t, 2, summary.TotalAccounts)
	assert.Equal(t, 120, summary.TotalTransactions)
	assert.Equal(t, 50000.00, summary.TotalIncome)
	assert.Equal(t, 30000.50, summary.TotalExpense)

	assert.Len(t, summary.ByCategory, 1)
	assert.Equal(t, "Food", summary.ByCategory[0].CategoryName)
	assert.Equal(t, 8, summary.ByCategory[0].Count)

	assert.Len(t, summary.IncomeByCategory, 1)
	assert.Equal(t, "Salary", summary.IncomeByCategory[0].CategoryName)

	assert.Len(t, summary.MonthlyTrend, 1)
	assert.Equal(t, "2026-07", summary.MonthlyTrend[0].Month)

	assert.Len(t, summary.RecentTransactions, 1)
	assert.Equal(t, "Zomato order", summary.RecentTransactions[0].Description)
	assert.Equal(t, "Food", summary.RecentTransactions[0].CategoryName)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetDashboardSummaryWithDateFilter(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	r := newDashboardTestRouter()
	userID := testUserID()
	dateFrom := "2026-07-01"
	dateTo := "2026-07-31"
	filterArgs := []interface{}{userID, dateFrom, dateTo}

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions WHERE user_id").
		WithArgs(filterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(10))
	mock.ExpectQuery("type = 'credit' AND user_id").
		WithArgs(filterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{"sum"}).AddRow(1000.00))
	mock.ExpectQuery("type = 'debit' AND user_id").
		WithArgs(filterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{"sum"}).AddRow(400.00))
	mock.ExpectQuery("t.type = 'debit' AND t.user_id").
		WithArgs(filterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "total", "count"}))
	mock.ExpectQuery("t.type = 'credit' AND t.user_id").
		WithArgs(filterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "total", "count"}))
	mock.ExpectQuery("SELECT TO_CHAR\\(date, 'YYYY-MM'\\) as month").
		WithArgs(filterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{"month", "income", "expense"}))
	mock.ExpectQuery("SELECT t.id, t.account_id, t.date, t.description, t.amount, t.type").
		WithArgs(filterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "account_id", "date", "description", "amount", "type",
			"category_id", "tags", "notes", "payee_id", "payee", "created_at",
			"account_name", "category_name", "category_icon", "category_color",
		}))

	req, _ := http.NewRequest(http.MethodGet, "/dashboard/summary?dateFrom="+dateFrom+"&dateTo="+dateTo, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetDashboardSummaryWithAccountFilter(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	r := newDashboardTestRouter()
	userID := testUserID()
	accountID := uuid.New()
	filterArgs := []interface{}{userID, accountID.String()}

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions WHERE user_id").
		WithArgs(filterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(5))
	mock.ExpectQuery("type = 'credit' AND user_id").
		WithArgs(filterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{"sum"}).AddRow(8000.00))
	mock.ExpectQuery("type = 'debit' AND user_id").
		WithArgs(filterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{"sum"}).AddRow(2000.00))
	mock.ExpectQuery("t.type = 'debit' AND t.user_id").
		WithArgs(filterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "total", "count"}))
	mock.ExpectQuery("t.type = 'credit' AND t.user_id").
		WithArgs(filterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "total", "count"}))
	mock.ExpectQuery("SELECT TO_CHAR\\(date, 'YYYY-MM'\\) as month").
		WithArgs(filterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{"month", "income", "expense"}))
	mock.ExpectQuery("SELECT t.id, t.account_id, t.date, t.description, t.amount, t.type").
		WithArgs(filterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "account_id", "date", "description", "amount", "type",
			"category_id", "tags", "notes", "payee_id", "payee", "created_at",
			"account_name", "category_name", "category_icon", "category_color",
		}))

	req, _ := http.NewRequest(http.MethodGet, "/dashboard/summary?accountId="+accountID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetDashboardSummaryIncomeQueryError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	r := newDashboardTestRouter()
	userID := testUserID()

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("type = 'credit' AND user_id").
		WithArgs(userID).
		WillReturnError(assert.AnError)

	req, _ := http.NewRequest(http.MethodGet, "/dashboard/summary", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}
