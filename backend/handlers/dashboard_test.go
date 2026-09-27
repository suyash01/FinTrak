package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/fintrak/backend/internal/money"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newDashboardTestRouter(srv *Server) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.GET("/dashboard/summary", srv.GetDashboardSummary)
	return r
}

// The window that cuts the top 15 per currency, spelled out rather than left
// loose because the tiebreak is the point of it: without c.name and c.id after
// SUM(t.amount) DESC, which 15 categories survive an equal-total tie is
// arbitrary and can differ between two identical requests. Every category-query
// expectation in this file matches it, so dropping the tiebreak fails a test
// rather than silently making the list unstable.
const catWindowRegex = `ROW_NUMBER\(\) OVER \(PARTITION BY COALESCE\(NULLIF\(a\.currency, ''\), 'INR'\) ORDER BY SUM\(t\.amount\) DESC, c\.name, c\.id\)`

// The account-currency predicate, as it has to appear in every transaction-backed
// section of the summary when ?currency= is supplied: one expression, one
// placeholder. A section that dropped it would describe a wider window than the
// stat cards beside it, which is the contradiction the test below exists to
// catch, so each of those expectations matches this and binds the code.
//
// Note what this regex cannot also do: pin that the placeholder appears exactly
// once. That is not an oversight but a property of the matcher — pgxmock's
// QueryMatcherRegexp runs stripQuery over the actual statement first, collapsing
// every whitespace run to a single space, so no pattern can tell `= $2` from
// `= $2 $2` by what follows it, and a bare `= \$2` matches inside the malformed
// `= $2 $2` too. That is how a statement PostgreSQL rejects passed thirteen tasks
// and a per-task review: a matcher compares strings and cannot parse one, so
// tightening this pattern is not available as a fix for anything.
const currencyFilterRegex = `COALESCE\(NULLIF\(a\.currency, ''\), 'INR'\) = \$2`

func TestGetDashboardSummary(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	r := newDashboardTestRouter(srv)
	userID := testUserID()
	now := time.Now()

	catID := uuid.New()
	accountID := uuid.New()
	txnID := uuid.New()

	// 1. Total accounts
	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))

	// 2. Transaction count. A count carries no currency, so the income and
	// expense totals come from the currency scope in step 3 instead.
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(120))

	// 3. The currency scope: the headline totals and the accounts behind them.
	mock.ExpectQuery("FROM accounts a\\s+LEFT JOIN transactions t").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
			AddRow(accountID, "Savings", "INR", money.FromFloat(50000.00), money.FromFloat(30000.50)))

	// 4. Expense by category. The regexp pins the window's partition expression
	// and its name/id tiebreak, not just that a debit query ran.
	mock.ExpectQuery(catWindowRegex + "[\\s\\S]*t\\.type = 'debit' AND t\\.user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "currency", "total", "count"}).
			AddRow(catID.String(), "Food", "#f97316", "utensils", "INR", money.FromFloat(12000.00), 8))

	// 5. Income by category
	mock.ExpectQuery(catWindowRegex + "[\\s\\S]*t\\.type = 'credit' AND t\\.user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "currency", "total", "count"}).
			AddRow(catID.String(), "Salary", "#22c55e", "wallet", "INR", money.FromFloat(50000.00), 1))

	// 6. Monthly trend
	mock.ExpectQuery("TO_CHAR\\(t.date, 'YYYY-MM'\\)").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"month", "currency", "income", "expense"}).
			AddRow("2026-07", "INR", money.FromFloat(50000.00), money.FromFloat(30000.50)))

	// 7. Recent transactions. The tags column is coalesced so a pre-existing
	// NULL row serializes as an array, not null.
	mock.ExpectQuery(regexp.QuoteMeta("COALESCE(t.tags, '{}') as tags")).
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "account_id", "date", "description", "amount", "type",
			"category_id", "tags", "notes", "payee_id", "payee", "created_at",
			"account_name", "category_name", "category_icon", "category_color",
		}).
			AddRow(txnID, accountID, now, "Zomato order", 450.00, "debit",
				nil, []string{"food"}, "", nil, "Zomato", now,
				"Savings", "Food", "utensils", "#f97316"))

	mock.ExpectCommit()

	req, _ := http.NewRequest(http.MethodGet, "/dashboard/summary", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var summary models.DashboardSummary
	err = json.Unmarshal(w.Body.Bytes(), &summary)
	assert.NoError(t, err)

	assert.Equal(t, 2, summary.TotalAccounts)
	assert.Equal(t, 120, summary.TotalTransactions)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(50000.00)}, summary.TotalIncome)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(30000.50)}, summary.TotalExpense)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(19999.50)}, summary.TotalNet)
	assert.Equal(t, []string{"INR"}, summary.CurrencyScope.Currencies)

	if _, _, ok := summary.TotalIncome.Single(); !ok {
		t.Error("a single-currency total did not report as single")
	}

	assert.Len(t, summary.ByCategory, 1)
	assert.Equal(t, "Food", summary.ByCategory[0].CategoryName)
	assert.Equal(t, 8, summary.ByCategory[0].Count)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(12000.00)}, summary.ByCategory[0].Total)

	assert.Len(t, summary.IncomeByCategory, 1)
	assert.Equal(t, "Salary", summary.IncomeByCategory[0].CategoryName)

	assert.Len(t, summary.MonthlyTrend, 1)
	assert.Equal(t, "2026-07", summary.MonthlyTrend[0].Month)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(50000.00)}, summary.MonthlyTrend[0].Income)

	assert.Len(t, summary.RecentTransactions, 1)
	assert.Equal(t, "Zomato order", summary.RecentTransactions[0].Description)
	assert.Equal(t, "Food", summary.RecentTransactions[0].CategoryName)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// The bug this endpoint had: with no account filter, a window over a USD
// account and an INR account summed dollars into rupees. The response has to
// refuse the total rather than report it.
func TestGetDashboardSummaryRefusesToCombineCurrencies(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)
	r := newDashboardTestRouter(srv)
	userID := testUserID()

	acctINR := uuid.New()
	acctUSD := uuid.New()

	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(9))
	// The scope query: two accounts, two currencies.
	mock.ExpectQuery("FROM accounts a\\s+LEFT JOIN transactions t").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
			AddRow(acctINR, "Salary", "INR", money.FromFloat(50000), money.FromFloat(30000.50)).
			AddRow(acctUSD, "Travel card", "USD", 0, money.FromFloat(80)))
	// Category and trend queries now carry the account's currency.
	mock.ExpectQuery("a.currency").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "currency", "total", "count"}).
			AddRow("c1", "Food", "#f00", "food", "INR", money.FromFloat(12000), 8))
	mock.ExpectQuery("a.currency").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "currency", "total", "count"}))
	mock.ExpectQuery("TO_CHAR\\(t.date, 'YYYY-MM'\\)").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"month", "currency", "income", "expense"}).
			AddRow("2026-07", "INR", money.FromFloat(50000), money.FromFloat(30000.50)).
			AddRow("2026-07", "USD", 0, money.FromFloat(80)))
	mock.ExpectQuery("SELECT t.id, t.account_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "account_id", "date", "description", "amount", "type",
			"category_id", "tags", "notes", "payee_id", "payee", "created_at",
			"account_name", "category_name", "category_icon", "category_color",
		}))
	mock.ExpectCommit()

	req, _ := http.NewRequest(http.MethodGet, "/dashboard/summary", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var summary models.DashboardSummary
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &summary))

	// Every amount is a map, keyed by the currency that produced it.
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(50000)}, summary.TotalIncome)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(30000.50), "USD": money.FromFloat(80)}, summary.TotalExpense)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(19999.50), "USD": -money.FromFloat(80)}, summary.TotalNet)

	// A two-currency total refuses to report as a single number, which is what
	// stops a client from adding 50000 rupees to 80 dollars. The income side is
	// genuinely one currency here - the USD account never earned anything - so
	// the refusal is asserted on the sides that really do span two.
	if _, _, ok := summary.TotalExpense.Single(); ok {
		t.Error("a two-currency expense total reported as a single amount")
	}
	if _, _, ok := summary.TotalNet.Single(); ok {
		t.Error("a two-currency net reported as a single amount")
	}
	assert.Equal(t, []string{"INR", "USD"}, summary.CurrencyScope.Currencies)
	assert.Len(t, summary.CurrencyScope.Accounts, 2)

	// One month, two currencies: the trend folds into a single MonthlyData whose
	// two sides each hold both currencies, rather than two July entries.
	assert.Len(t, summary.MonthlyTrend, 1)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(50000)}, summary.MonthlyTrend[0].Income)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(30000.50), "USD": money.FromFloat(80)}, summary.MonthlyTrend[0].Expense)

	// The body carries a currency-keyed object for each of them: not a bare
	// number, and not an absent field either, which a "is it a float?" check
	// would wave through while the name quietly stopped being sent.
	var raw map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
	for _, field := range []string{"totalIncome", "totalExpense", "totalNet"} {
		amounts, isObject := raw[field].(map[string]any)
		if !isObject || len(amounts) == 0 {
			t.Errorf("%s = %v, want a currency-keyed object", field, raw[field])
		}
	}

	assert.NoError(t, mock.ExpectationsWereMet())
}

// A ?currency= filter that reached only the stat cards would leave the trend and
// the category breakdowns describing a wider window than the totals beside them:
// the client renders USD 80.00 next to a July bar that includes someone's rupees
// and neither number is wrong on its own. So the filter has to reach every
// transaction-backed section, and this pins that it does.
//
// The row values below are what a filtered database returns - a mock matches the
// statement, it does not execute it, so the guard is that every expectation
// requires the same currency predicate in the same query and binds the same
// normalised code. Drop the predicate from any one section and its expectation
// stops matching, the handler answers 500, and this fails.
func TestGetDashboardSummaryNarrowsEverySectionToTheCurrencyFilter(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)
	r := newDashboardTestRouter(srv)
	userID := testUserID()
	acctUSD := uuid.New()

	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))

	// 1. The count.
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t[\\s\\S]*"+currencyFilterRegex).
		WithArgs(userID, "USD").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(4))

	// 2. The scope, which carries the same predicate.
	mock.ExpectQuery("FROM accounts a\\s+LEFT JOIN transactions t[\\s\\S]*"+currencyFilterRegex).
		WithArgs(userID, "USD").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
			AddRow(acctUSD, "Travel card", "USD", money.FromFloat(120), money.FromFloat(80)))

	// 3/4. Both category breakdowns.
	mock.ExpectQuery(catWindowRegex+"[\\s\\S]*t\\.type = 'debit'[\\s\\S]*"+currencyFilterRegex).
		WithArgs(userID, "USD").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "currency", "total", "count"}).
			AddRow("c1", "Travel", "#f00", "plane", "USD", money.FromFloat(80), 3))
	mock.ExpectQuery(catWindowRegex+"[\\s\\S]*t\\.type = 'credit'[\\s\\S]*"+currencyFilterRegex).
		WithArgs(userID, "USD").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "currency", "total", "count"}).
			AddRow("c2", "Consulting", "#0f0", "briefcase", "USD", money.FromFloat(120), 1))

	// 5. The trend.
	mock.ExpectQuery("TO_CHAR\\(t.date, 'YYYY-MM'\\)[\\s\\S]*"+currencyFilterRegex).
		WithArgs(userID, "USD").
		WillReturnRows(pgxmock.NewRows([]string{"month", "currency", "income", "expense"}).
			AddRow("2026-07", "USD", money.FromFloat(120), money.FromFloat(80)))

	// 6. The recent list, which is a transaction-backed section too.
	mock.ExpectQuery("SELECT t.id, t.account_id[\\s\\S]*"+currencyFilterRegex).
		WithArgs(userID, "USD").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "account_id", "date", "description", "amount", "type",
			"category_id", "tags", "notes", "payee_id", "payee", "created_at",
			"account_name", "category_name", "category_icon", "category_color",
		}))
	mock.ExpectCommit()

	// Lower case on purpose: the bound argument is the normalised "USD", so a
	// regression that forwarded the raw parameter would match nothing and report
	// a quiet month with no error anywhere.
	req, _ := http.NewRequest(http.MethodGet, "/dashboard/summary?currency=usd", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var summary models.DashboardSummary
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &summary))

	assert.Equal(t, 4, summary.TotalTransactions)
	assert.Equal(t, models.CurrencyAmounts{"USD": money.FromFloat(120)}, summary.TotalIncome)
	assert.Equal(t, models.CurrencyAmounts{"USD": money.FromFloat(80)}, summary.TotalExpense)
	assert.Equal(t, models.CurrencyAmounts{"USD": money.FromFloat(40)}, summary.TotalNet)
	assert.Equal(t, []string{"USD"}, summary.CurrencyScope.Currencies)

	// The sections that the finding was about: with a USD filter, no amount in
	// them may name a second currency. Each of these holds exactly one key, so a
	// section that had quietly kept every currency would show two.
	require.Len(t, summary.ByCategory, 1)
	assert.Equal(t, models.CurrencyAmounts{"USD": money.FromFloat(80)}, summary.ByCategory[0].Total)
	require.Len(t, summary.IncomeByCategory, 1)
	assert.Equal(t, models.CurrencyAmounts{"USD": money.FromFloat(120)}, summary.IncomeByCategory[0].Total)
	require.Len(t, summary.MonthlyTrend, 1)
	assert.Equal(t, models.CurrencyAmounts{"USD": money.FromFloat(120)}, summary.MonthlyTrend[0].Income)
	assert.Equal(t, models.CurrencyAmounts{"USD": money.FromFloat(80)}, summary.MonthlyTrend[0].Expense)

	// Every amount in the response is single-currency, so a client can read all
	// of them as plain numbers without ever combining two of them.
	for name, amounts := range map[string]models.CurrencyAmounts{
		"totalIncome":    summary.TotalIncome,
		"totalExpense":   summary.TotalExpense,
		"totalNet":       summary.TotalNet,
		"byCategory":     summary.ByCategory[0].Total,
		"incomeCategory": summary.IncomeByCategory[0].Total,
		"monthlyIncome":  summary.MonthlyTrend[0].Income,
		"monthlyExpense": summary.MonthlyTrend[0].Expense,
	} {
		if code, _, ok := amounts.Single(); !ok || code != "USD" {
			t.Errorf("%s = %v, want a single USD amount", name, amounts)
		}
	}

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetDashboardSummaryWithDateFilter(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	r := newDashboardTestRouter(srv)
	userID := testUserID()
	dateFrom := "2026-07-01"
	dateTo := "2026-07-31"
	filterArgs := []interface{}{userID, dateFrom, dateTo}

	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions").
		WithArgs(filterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(10))
	// The scope query carries the same window, in its JOIN's ON clause.
	mock.ExpectQuery("FROM accounts a\\s+LEFT JOIN transactions t").
		WithArgs(filterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
			AddRow(uuid.New(), "Savings", "INR", money.FromFloat(1000.00), money.FromFloat(400.00)))
	mock.ExpectQuery("t.type = 'debit' AND t.user_id").
		WithArgs(filterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "currency", "total", "count"}))
	mock.ExpectQuery("t.type = 'credit' AND t.user_id").
		WithArgs(filterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "currency", "total", "count"}))
	mock.ExpectQuery("TO_CHAR\\(t.date, 'YYYY-MM'\\)").
		WithArgs(filterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{"month", "currency", "income", "expense"}))
	mock.ExpectQuery("SELECT t.id, t.account_id, t.date, t.description, t.amount, t.type").
		WithArgs(filterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "account_id", "date", "description", "amount", "type",
			"category_id", "tags", "notes", "payee_id", "payee", "created_at",
			"account_name", "category_name", "category_icon", "category_color",
		}))
	mock.ExpectCommit()

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
	srv := newTestServer(mock)

	r := newDashboardTestRouter(srv)
	userID := testUserID()
	accountID := uuid.New()
	filterArgs := []interface{}{userID, accountID.String()}

	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions").
		WithArgs(filterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(5))
	// An account filter pins the response to one account, so the scope carries
	// exactly one currency.
	mock.ExpectQuery("FROM accounts a\\s+LEFT JOIN transactions t").
		WithArgs(filterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
			AddRow(accountID, "Savings", "INR", money.FromFloat(8000.00), money.FromFloat(2000.00)))
	mock.ExpectQuery("t.type = 'debit' AND t.user_id").
		WithArgs(filterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "currency", "total", "count"}))
	mock.ExpectQuery("t.type = 'credit' AND t.user_id").
		WithArgs(filterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "currency", "total", "count"}))
	mock.ExpectQuery("TO_CHAR\\(t.date, 'YYYY-MM'\\)").
		WithArgs(filterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{"month", "currency", "income", "expense"}))
	mock.ExpectQuery("SELECT t.id, t.account_id, t.date, t.description, t.amount, t.type").
		WithArgs(filterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "account_id", "date", "description", "amount", "type",
			"category_id", "tags", "notes", "payee_id", "payee", "created_at",
			"account_name", "category_name", "category_icon", "category_color",
		}))
	mock.ExpectCommit()

	req, _ := http.NewRequest(http.MethodGet, "/dashboard/summary?accountId="+accountID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// The income and expense totals come from the currency scope query, so this is
// where the totals can now fail.
func TestGetDashboardSummaryIncomeQueryError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	r := newDashboardTestRouter(srv)
	userID := testUserID()

	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(120))
	mock.ExpectQuery("FROM accounts a\\s+LEFT JOIN transactions t").
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
	srv := newTestServer(mock)

	r := newDashboardTestRouter(srv)
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

	// The billing-cycle view's shared filter fragment: the cycle window, then the
	// account, then the currency filter if one was asked for (none here). The
	// account id is bound as a uuid here, not as the query string the unbounded
	// view passes, because the handler already parsed it.
	cycleFilterArgs := []interface{}{userID, windowStart, windowEnd, accountID}

	// 1. Account billing-day lookup
	mock.ExpectQuery("SELECT a.billing_day").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"billing_day"}).AddRow(intPtr(5)))

	// 2. ensureBillingCycles internals (its own transaction)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT end_date FROM billing_cycles WHERE account_id").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"end_date"}).
			AddRow(end1).AddRow(end2).AddRow(end3))
	mock.ExpectQuery("MIN\\(date\\)").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"min"}).AddRow(date(2026, 5, 10)))
	covered := pgxmock.NewRows([]string{"end_date"})
	for _, ms := range billingCycleMonths(date(2026, 5, 10), dateOnly(time.Now()), 5) {
		_, end := cycleDates(ms, 5)
		covered.AddRow(end)
	}
	mock.ExpectQuery("SELECT end_date FROM billing_cycles WHERE account_id").
		WithArgs(accountID, userID).
		WillReturnRows(covered)
	mock.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM transactions WHERE account_id").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectExec("UPDATE transactions t SET billing_cycle_id").
		WithArgs(accountID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectCommit()

	// 3. listBillingCycles (read snapshot)
	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
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

	// 5. The currency scope over the cycle window. One account, so exactly one
	// currency; the account id arrives from the query string, hence String().
	mock.ExpectQuery("FROM accounts a\\s+LEFT JOIN transactions t").
		WithArgs(userID, windowStart, windowEnd, accountID.String()).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
			AddRow(accountID, "Savings", "INR", money.FromFloat(21000.00), money.FromFloat(6700.50)))

	// 6. Transaction count across all displayed cycles. The window, the account
	// and the (absent) currency filter are one shared fragment, so the count,
	// the categories and the recent list all bind the same arguments in the same
	// order - the count used to bind them differently and attribute by cycle id.
	// The regexp pins the accounts join, which is what carries that filter.
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t\\s+JOIN accounts a ON a\\.id = t\\.account_id").
		WithArgs(cycleFilterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(18))

	// 7. Per-cycle trend. The transactions are joined by the cycle's own date
	// range rather than by t.billing_cycle_id, so the bars and the stat cards
	// count the same transactions; the regexp pins that join so putting the
	// cycle id back fails here.
	mock.ExpectQuery("SELECT bc.label, bc.start_date, bc.end_date[\\s\\S]*t\\.date >= bc\\.start_date AND t\\.date <= bc\\.end_date").
		WithArgs(accountID, userID, windowStart, windowEnd).
		WillReturnRows(pgxmock.NewRows([]string{"label", "start_date", "end_date", "currency", "income", "expense"}).
			AddRow("Jun 2026", start1, end1, "INR", money.FromFloat(5000.00), money.FromFloat(1200.00)).
			AddRow("Jul 2026", start2, end2, "INR", money.FromFloat(6000.00), money.FromFloat(2400.00)).
			AddRow("Aug 2026", start3, end3, "INR", money.FromFloat(10000.00), money.FromFloat(3100.50)))

	// 8. Expense by category (cycle window)
	mock.ExpectQuery("t.type = 'debit' AND t.user_id").
		WithArgs(cycleFilterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "currency", "total", "count"}).
			AddRow(catID.String(), "Food", "#f97316", "utensils", "INR", money.FromFloat(2400.00), 6))

	// 9. Income by category (cycle window)
	mock.ExpectQuery("t.type = 'credit' AND t.user_id").
		WithArgs(cycleFilterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "currency", "total", "count"}).
			AddRow(catID.String(), "Salary", "#22c55e", "wallet", "INR", money.FromFloat(10000.00), 1))

	// 10. Recent transactions (across the displayed cycle window). The tags
	// column is coalesced so a pre-existing NULL row serializes as an array.
	mock.ExpectQuery(regexp.QuoteMeta("COALESCE(t.tags, '{}') as tags")).
		WithArgs(cycleFilterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "account_id", "date", "description", "amount", "type",
			"category_id", "tags", "notes", "payee_id", "payee", "created_at",
			"account_name", "category_name", "category_icon", "category_color",
		}).
			AddRow(txnID, accountID, date(2026, 8, 1), "Zomato order", 450.00, "debit",
				nil, []string{"food"}, "", nil, "Zomato", date(2026, 8, 1),
				"Savings", "Food", "utensils", "#f97316"))

	mock.ExpectCommit()

	req, _ := http.NewRequest(http.MethodGet, "/dashboard/summary?groupBy=billing_cycle&accountId="+accountID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var summary models.DashboardSummary
	err = json.Unmarshal(w.Body.Bytes(), &summary)
	assert.NoError(t, err)

	assert.Equal(t, 3, summary.TotalAccounts)
	assert.Equal(t, 18, summary.TotalTransactions)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(21000.00)}, summary.TotalIncome)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(6700.50)}, summary.TotalExpense)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(14299.50)}, summary.TotalNet)
	// One account, so the scope names exactly one currency and the totals can
	// still be read as single amounts.
	assert.Equal(t, []string{"INR"}, summary.CurrencyScope.Currencies)
	if _, _, ok := summary.TotalNet.Single(); !ok {
		t.Error("an account-scoped net did not report as a single amount")
	}

	assert.NotNil(t, summary.CurrentCycle)
	assert.Equal(t, "Aug 2026", summary.CurrentCycle.Label)
	assert.Equal(t, cycle3, summary.CurrentCycle.ID)

	assert.Len(t, summary.BillingCycleTrend, 3)
	assert.Equal(t, "Jul 2026", summary.BillingCycleTrend[1].Label)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(6000.00)}, summary.BillingCycleTrend[1].Income)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(2400.00)}, summary.BillingCycleTrend[1].Expense)

	assert.Len(t, summary.ByCategory, 1)
	assert.Equal(t, "Food", summary.ByCategory[0].CategoryName)
	assert.Len(t, summary.IncomeByCategory, 1)
	assert.Equal(t, "Salary", summary.IncomeByCategory[0].CategoryName)

	assert.Len(t, summary.RecentTransactions, 1)
	assert.Equal(t, "Zomato order", summary.RecentTransactions[0].Description)
	assert.Equal(t, "Food", summary.RecentTransactions[0].CategoryName)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// The billing-cycle view narrows on the same ?currency= value the unbounded view
// does, and the trend is the reason this needs its own test: that query numbers
// its arguments itself (account, user, window, window, currency) instead of
// taking the shared fragment, so its predicate sits at a fixed fifth
// placeholder. Nothing but a test would notice if that constant and the argument
// appended after it drifted apart, and a wrong placeholder is a query error
// rather than a wrong number - which is the best available failure mode, but
// still one to catch here rather than in production.
func TestGetDashboardSummaryBillingCycleNarrowsToTheCurrencyFilter(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	r := newDashboardTestRouter(srv)
	userID := testUserID()
	accountID := uuid.New()
	cycleID := uuid.New()
	start := time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	windowStart, windowEnd := "2026-07-06", "2026-08-05"

	// The shared fragment's currency predicate is the fourth condition, so $5.
	// The scope query numbers the same three filters and reaches $5 too. This view
	// builds its own copy of the fragment, so it carries the same caveat as
	// currencyFilterRegex: the pattern pins the spelling, and nothing here pins
	// the placeholder count.
	const filterAt5 = `COALESCE\(NULLIF\(a\.currency, ''\), 'INR'\) = \$5`

	mock.ExpectQuery("SELECT a.billing_day").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"billing_day"}).AddRow(intPtr(5)))
	expectBillingCyclesUpToDate(mock, userID, accountID, 5)

	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	mock.ExpectQuery("SELECT bc.id, bc.start_date, bc.end_date, bc.label").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "start_date", "end_date", "label", "net_activity", "txn_count"}).
			AddRow(cycleID, start, end, "Aug 2026", 100.00, 2))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("FROM accounts a\\s+LEFT JOIN transactions t[\\s\\S]*"+filterAt5).
		WithArgs(userID, windowStart, windowEnd, accountID.String(), "INR").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
			AddRow(accountID, "Savings", "INR", money.FromFloat(100), money.FromFloat(40)))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t[\\s\\S]*"+filterAt5).
		WithArgs(userID, windowStart, windowEnd, accountID, "INR").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectQuery("SELECT bc.label, bc.start_date, bc.end_date[\\s\\S]*"+filterAt5).
		WithArgs(accountID, userID, windowStart, windowEnd, "INR").
		WillReturnRows(pgxmock.NewRows([]string{"label", "start_date", "end_date", "currency", "income", "expense"}).
			AddRow("Aug 2026", start, end, "INR", money.FromFloat(100), money.FromFloat(40)))
	mock.ExpectQuery("t.type = 'debit'[\\s\\S]*"+filterAt5).
		WithArgs(userID, windowStart, windowEnd, accountID, "INR").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "currency", "total", "count"}))
	mock.ExpectQuery("t.type = 'credit'[\\s\\S]*"+filterAt5).
		WithArgs(userID, windowStart, windowEnd, accountID, "INR").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "currency", "total", "count"}))
	mock.ExpectQuery(regexp.QuoteMeta("COALESCE(t.tags, '{}') as tags")+"[\\s\\S]*"+filterAt5).
		WithArgs(userID, windowStart, windowEnd, accountID, "INR").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "account_id", "date", "description", "amount", "type",
			"category_id", "tags", "notes", "payee_id", "payee", "created_at",
			"account_name", "category_name", "category_icon", "category_color",
		}))
	mock.ExpectCommit()

	req, _ := http.NewRequest(http.MethodGet, "/dashboard/summary?groupBy=billing_cycle&accountId="+accountID.String()+"&currency=INR", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var summary models.DashboardSummary
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &summary))
	assert.Equal(t, 2, summary.TotalTransactions)
	assert.Equal(t, []string{"INR"}, summary.CurrencyScope.Currencies)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetDashboardSummaryBillingCycleNoBillingDay(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	r := newDashboardTestRouter(srv)
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
	srv := newTestServer(mock)

	r := newDashboardTestRouter(srv)

	req, _ := http.NewRequest(http.MethodGet, "/dashboard/summary?groupBy=billing_cycle", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}
