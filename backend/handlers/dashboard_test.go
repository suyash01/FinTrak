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

func TestGetDashboardSummaryBillingCycle(t *testing.T) {
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
	catID := uuid.New()
	txnID := uuid.New()

	date := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}
	start1, end1 := date(2026, 5, 6), date(2026, 6, 5)
	start2, end2 := date(2026, 6, 6), date(2026, 7, 5)
	start3, end3 := date(2026, 7, 6), date(2026, 8, 5)
	cycle1 := uuid.New()
	cycle2 := uuid.New()
	cycle3 := uuid.New()

	windowStart := "2026-05-06"
	windowEnd := "2026-08-05"

	// 1. Account billing-day lookup
	mock.ExpectQuery("SELECT a.billing_day").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"billing_day"}).AddRow(intPtr(5)))

	// 2. ensureBillingCycles internals
	mock.ExpectQuery("SELECT end_date FROM billing_cycles WHERE account_id").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"end_date"}).
			AddRow(end1).AddRow(end2).AddRow(end3))
	mock.ExpectQuery("SELECT MIN\\(date\\) FROM transactions WHERE account_id").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"min"}).AddRow(date(2026, 5, 10)))
	mock.ExpectQuery("SELECT MAX\\(end_date\\) FROM billing_cycles WHERE account_id").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"max"}).AddRow(date(2099, 1, 5)))
	mock.ExpectExec("UPDATE transactions t SET billing_cycle_id").
		WithArgs(accountID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	// 3. listBillingCycles
	mock.ExpectQuery("SELECT bc.id, bc.start_date, bc.end_date, bc.label").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "start_date", "end_date", "label", "net_activity", "txn_count"}).
			AddRow(cycle1, start1, end1, "Jun 2026", 1200.00, 4).
			AddRow(cycle2, start2, end2, "Jul 2026", 2400.00, 6).
			AddRow(cycle3, start3, end3, "Aug 2026", 3100.50, 8))

	// 4. Total accounts
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(3))

	// 5. Window totals (stat cards) across all displayed cycles
	mock.ExpectQuery("SELECT COUNT\\(t.id\\),").
		WithArgs(userID, accountID, windowStart, windowEnd).
		WillReturnRows(pgxmock.NewRows([]string{"count", "income", "expense"}).
			AddRow(18, 21000.00, 6700.50))

	// 6. Per-cycle trend
	mock.ExpectQuery("SELECT bc.label, bc.start_date, bc.end_date").
		WithArgs(accountID, userID, windowStart, windowEnd).
		WillReturnRows(pgxmock.NewRows([]string{"label", "start_date", "end_date", "income", "expense"}).
			AddRow("Jun 2026", start1, end1, 5000.00, 1200.00).
			AddRow("Jul 2026", start2, end2, 6000.00, 2400.00).
			AddRow("Aug 2026", start3, end3, 10000.00, 3100.50))

	// 7. Expense by category (cycle window)
	mock.ExpectQuery("t.type = 'debit' AND t.user_id").
		WithArgs(userID, windowStart, windowEnd, accountID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "total", "count"}).
			AddRow(catID, "Food", "#f97316", "utensils", 2400.00, 6))

	// 8. Income by category (cycle window)
	mock.ExpectQuery("t.type = 'credit' AND t.user_id").
		WithArgs(userID, windowStart, windowEnd, accountID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "total", "count"}).
			AddRow(catID, "Salary", "#22c55e", "wallet", 10000.00, 1))

	// 9. Recent transactions (across the displayed cycle window)
	mock.ExpectQuery("SELECT t.id, t.account_id, t.date, t.description, t.amount, t.type").
		WithArgs(userID, accountID, windowStart, windowEnd).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "account_id", "date", "description", "amount", "type",
			"category_id", "tags", "notes", "payee_id", "payee", "created_at",
			"account_name", "category_name", "category_icon", "category_color",
		}).
			AddRow(txnID, accountID, date(2026, 8, 1), "Zomato order", 450.00, "debit",
				nil, []string{"food"}, "", nil, "Zomato", date(2026, 8, 1),
				"Savings", "Food", "utensils", "#f97316"))

	req, _ := http.NewRequest(http.MethodGet, "/dashboard/summary?groupBy=billing_cycle&accountId="+accountID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var summary models.DashboardSummary
	err = json.Unmarshal(w.Body.Bytes(), &summary)
	assert.NoError(t, err)

	assert.Equal(t, 3, summary.TotalAccounts)
	assert.Equal(t, 18, summary.TotalTransactions)
	assert.Equal(t, 21000.00, summary.TotalIncome)
	assert.Equal(t, 6700.50, summary.TotalExpense)

	assert.NotNil(t, summary.CurrentCycle)
	assert.Equal(t, "Aug 2026", summary.CurrentCycle.Label)
	assert.Equal(t, cycle3, summary.CurrentCycle.ID)

	assert.Len(t, summary.BillingCycleTrend, 3)
	assert.Equal(t, "Jul 2026", summary.BillingCycleTrend[1].Label)
	assert.Equal(t, 6000.00, summary.BillingCycleTrend[1].Income)
	assert.Equal(t, 2400.00, summary.BillingCycleTrend[1].Expense)

	assert.Len(t, summary.ByCategory, 1)
	assert.Equal(t, "Food", summary.ByCategory[0].CategoryName)
	assert.Len(t, summary.IncomeByCategory, 1)
	assert.Equal(t, "Salary", summary.IncomeByCategory[0].CategoryName)

	assert.Len(t, summary.RecentTransactions, 1)
	assert.Equal(t, "Zomato order", summary.RecentTransactions[0].Description)
	assert.Equal(t, "Food", summary.RecentTransactions[0].CategoryName)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetDashboardSummaryBillingCycleNoBillingDay(t *testing.T) {
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

	mock.ExpectQuery("SELECT a.billing_day").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"billing_day"}).AddRow(nil))

	req, _ := http.NewRequest(http.MethodGet, "/dashboard/summary?groupBy=billing_cycle&accountId="+accountID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetDashboardSummaryBillingCycleMissingAccount(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	r := newDashboardTestRouter()

	req, _ := http.NewRequest(http.MethodGet, "/dashboard/summary?groupBy=billing_cycle", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}
