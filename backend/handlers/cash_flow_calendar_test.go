package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

func newCashFlowCalendarTestRouter(srv *Server) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.GET("/dashboard/cash-flow-calendar", srv.GetCashFlowCalendar)
	return r
}

// The account-currency expression, as it has to appear in the SELECT projection,
// the GROUP BY and the ?currency= predicate of the daily query. The expectations
// below match these rather than a loose "a.currency" because pgxmock compares
// arguments with reflect.DeepEqual: a respelled expression binds identically, so
// it would sail through a loose regexp while disagreeing with the scope query the
// same response's currencyScope and window totals were folded from - and would
// drop every account whose currency is merely unset out of an explicit
// ?currency=INR report, which the projection would still call INR.
const (
	calendarCurrencyRegex     = `COALESCE\(NULLIF\(a\.currency, ''\), 'INR'\)`
	calendarCurrencyProjRegex = calendarCurrencyRegex + ` AS currency`
	// The currency is the second select expression and the second grouping term,
	// so both the projection and the GROUP BY have to spell it: a statement
	// grouping the date alone lets the database hand back one amount per date
	// that has already added dollars to rupees. The head runs to the base WHERE
	// and the predicate is spliced between head and tail exactly where the
	// handler splices it, so the unfiltered expectation reuses the same pieces.
	calendarDailyHead = `(?s)SELECT t\.date, ` + calendarCurrencyProjRegex +
		`, COALESCE\(SUM\(CASE WHEN t\.type = 'credit'[\s\S]*WHERE t\.user_id = \$1`
	calendarDailyTail = `[\s\S]*GROUP BY t\.date, ` + calendarCurrencyRegex + `\s+ORDER BY t\.date`
	// The daily query's own filter fragment, in the order flowFilter renders it:
	// the window, then the account, then the currency.
	calendarWindowRegex = `t\.date >= \$2 AND t\.date <= \$3 AND a\.id = \$4`
	// The shared currencyScope statement, which puts the window in the join's ON
	// so a quiet account still names its currency.
	calendarScopeRegex = `FROM accounts a\s+LEFT JOIN transactions t`
)

// cashFlowDayCols is the daily query's row shape: the currency rides beside the
// date, so a day spanning two currencies arrives as two rows the fold joins.
func cashFlowDayCols() []string {
	return []string{"date", "currency", "income", "expense", "count"}
}

// cashFlowCurrencyPredAt is the currency predicate with the placeholder
// flowFilter reached. The shared fragment binds the window, then the account, then
// the currency, so a window of two bounds plus an account puts the code on $5.
func cashFlowCurrencyPredAt(placeholder int) string {
	return calendarCurrencyRegex + fmt.Sprintf(` = \$%d`, placeholder)
}

func TestGetCashFlowCalendar(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	srv := newTestServer(mock)

	userID := testUserID()
	acctID := uuid.New()
	dateFrom := "2024-06-01"
	dateTo := "2024-06-30"

	// The scope and the days are read in one snapshot, so the window totals and
	// the days they are the sum of cannot describe different ledgers; the begin
	// and commit are what pin that, and dropping either fails here.
	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	mock.ExpectQuery(calendarScopeRegex).
		WithArgs(userID, dateFrom, dateTo).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
			AddRow(acctID, "Savings", "INR", money.FromFloat(50000), money.FromFloat(1500)))
	mock.ExpectQuery(calendarDailyHead + `[\s\S]*` + calendarDailyTail).
		WithArgs(userID, dateFrom, dateTo).
		WillReturnRows(pgxmock.NewRows(cashFlowDayCols()).
			AddRow(time.Date(2024, 6, 2, 0, 0, 0, 0, time.UTC), "INR", money.FromFloat(50000), money.FromFloat(0), 1).
			AddRow(time.Date(2024, 6, 3, 0, 0, 0, 0, time.UTC), "INR", money.FromFloat(0), money.FromFloat(1500), 2))
	mock.ExpectCommit()

	req, _ := http.NewRequest(http.MethodGet, "/dashboard/cash-flow-calendar?dateFrom="+dateFrom+"&dateTo="+dateTo, nil)
	w := httptest.NewRecorder()
	newCashFlowCalendarTestRouter(srv).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var cal models.CashFlowCalendar
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &cal))

	require.Len(t, cal.Days, 2)
	assert.Equal(t, "2024-06-02", cal.Days[0].Date)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(50000)}, cal.Days[0].Income)
	// A day that only earned has no expense key: a zero contribution adds none,
	// and a missing key reads as zero.
	assert.Equal(t, models.CurrencyAmounts{}, cal.Days[0].Expense)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(50000)}, cal.Days[0].Net)
	assert.Equal(t, 1, cal.Days[0].Count)

	assert.Equal(t, "2024-06-03", cal.Days[1].Date)
	assert.Equal(t, models.CurrencyAmounts{}, cal.Days[1].Income)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(-1500)}, cal.Days[1].Net)
	assert.Equal(t, 2, cal.Days[1].Count)

	// The window totals come from the scope query, not from summing the day
	// rows here, so they and the dashboard's are computed by the same statement.
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(50000)}, cal.TotalIncome)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(1500)}, cal.TotalExpense)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(48500)}, cal.Net)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(50000)}, cal.MaxAbsNet)
	if _, _, ok := cal.Net.Single(); !ok {
		t.Error("a single-currency calendar net did not report as a single amount")
	}
	assert.Equal(t, []string{"INR"}, cal.CurrencyScope.Currencies)
	require.Len(t, cal.CurrencyScope.Accounts, 1)
	assert.Equal(t, "Savings", cal.CurrencyScope.Accounts[0].Name)
	assert.Empty(t, cal.Markers)
	assert.Empty(t, cal.Cycles)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// The bug this endpoint had: maxAbsNet was one number for the whole window, so a
// heatmap over a USD account and an INR one divided every day by the same
// denominator. A USD day of -300.00 then rendered as a flat cell next to an INR
// day of -50000.00, and the real deficit was invisible.
func TestGetCashFlowCalendarScalesEachCurrencySeparately(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	userID := testUserID()
	acctINR, acctUSD := uuid.New(), uuid.New()
	dateFrom, dateTo := "2024-06-01", "2024-06-30"
	day1 := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2024, 6, 2, 0, 0, 0, 0, time.UTC)

	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	mock.ExpectQuery(calendarScopeRegex).
		WithArgs(userID, dateFrom, dateTo).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
			AddRow(acctINR, "Salary", "INR", money.FromFloat(10000), money.FromFloat(50000)).
			AddRow(acctUSD, "Travel card", "USD", money.FromFloat(0), money.FromFloat(300)))
	// One day, two rows: the currency is in the grouping, so the database never
	// adds the dollar spend to the rupee income before a map exists to keep them
	// apart.
	mock.ExpectQuery(calendarDailyHead + `[\s\S]*` + calendarDailyTail).
		WithArgs(userID, dateFrom, dateTo).
		WillReturnRows(pgxmock.NewRows(cashFlowDayCols()).
			AddRow(day1, "INR", money.FromFloat(10000), money.FromFloat(0), 1).
			AddRow(day1, "USD", money.FromFloat(0), money.FromFloat(300), 1).
			AddRow(day2, "INR", money.FromFloat(0), money.FromFloat(50000), 2))
	mock.ExpectCommit()

	req, _ := http.NewRequest(http.MethodGet, "/dashboard/cash-flow-calendar?dateFrom="+dateFrom+"&dateTo="+dateTo, nil)
	w := httptest.NewRecorder()
	newCashFlowCalendarTestRouter(newTestServer(mock)).ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var cal models.CashFlowCalendar
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &cal))

	// One day, one entry: the currency is part of the amount, not of the day's
	// identity. A day on which one currency spent and another earned has a net in
	// both, and neither is a total of the other.
	require.Len(t, cal.Days, 2)
	assert.Equal(t, "2024-06-01", cal.Days[0].Date)
	assert.Equal(t, models.CurrencyAmounts{
		"INR": money.FromFloat(10000),
		"USD": money.FromFloat(-300),
	}, cal.Days[0].Net)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(10000)}, cal.Days[0].Income)
	assert.Equal(t, models.CurrencyAmounts{"USD": money.FromFloat(300)}, cal.Days[0].Expense)
	assert.Equal(t, 2, cal.Days[0].Count)
	if _, _, ok := cal.Days[0].Net.Single(); ok {
		t.Error("a two-currency day reported a single net")
	}

	// The second day only spent, so its income map is empty rather than
	// present-and-zero and its net is the negative of its expense.
	assert.Equal(t, "2024-06-02", cal.Days[1].Date)
	assert.Equal(t, models.CurrencyAmounts{}, cal.Days[1].Income)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(-50000)}, cal.Days[1].Net)

	// The point of the task: each currency carries its own scale, so the USD
	// cell is coloured by USD 300.00 and not by INR 50000.00. One cross-currency
	// maximum would put 50000.00 under both keys and flatten the dollar cell.
	assert.Equal(t, models.CurrencyAmounts{
		"INR": money.FromFloat(50000),
		"USD": money.FromFloat(300),
	}, cal.MaxAbsNet)

	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(10000)}, cal.TotalIncome)
	assert.Equal(t, models.CurrencyAmounts{
		"INR": money.FromFloat(50000),
		"USD": money.FromFloat(300),
	}, cal.TotalExpense)
	assert.Equal(t, models.CurrencyAmounts{
		"INR": money.FromFloat(-40000),
		"USD": money.FromFloat(-300),
	}, cal.Net)
	assert.Equal(t, []string{"INR", "USD"}, cal.CurrencyScope.Currencies)

	// The scale reaches the wire as an object keyed by currency; a bare number
	// here is the single cross-currency denominator this change removes.
	assert.Contains(t, w.Body.String(), `"maxAbsNet":{"INR":50000.00,"USD":300.00}`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ?currency= narrows the days and the scope with it, and the predicate is the
// same COALESCE(NULLIF(...)) the projection and the grouping use. A WHERE missing
// the NULLIF would exclude the very account the filter exists to find - the one
// whose currency is unset - while the rest of the same response still called it
// INR. The expectation below matches the spelling as well as the bound argument,
// so a respelled projection fails it.
func TestGetCashFlowCalendarNarrowsToTheCurrencyFilter(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	userID := testUserID()
	acctID := uuid.New()
	dateFrom, dateTo := "2024-06-01", "2024-06-30"

	// The shared filter binds the two bounds, then the account, then the
	// currency, so the code lands on $5 - and the scope query numbers the same
	// three filters in the same order, so it reaches $5 too.
	filterAt5 := cashFlowCurrencyPredAt(5)

	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	mock.ExpectQuery(calendarScopeRegex + `[\s\S]*AND a\.id = \$4 AND ` + filterAt5 + `[\s\S]*GROUP BY a.id, a.name, ` + calendarCurrencyRegex).
		WithArgs(userID, dateFrom, dateTo, acctID.String(), "INR").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
			AddRow(acctID, "Savings", "INR", money.FromFloat(50000), money.FromFloat(1500)))
	mock.ExpectQuery(calendarDailyHead + ` AND ` + calendarWindowRegex + ` AND ` + filterAt5 + calendarDailyTail).
		WithArgs(userID, dateFrom, dateTo, acctID.String(), "INR").
		WillReturnRows(pgxmock.NewRows(cashFlowDayCols()).
			AddRow(time.Date(2024, 6, 2, 0, 0, 0, 0, time.UTC), "INR", money.FromFloat(50000), money.FromFloat(0), 1))
	mock.ExpectCommit()
	// The overlay lookup is expected even though a ?currency= request does not
	// narrow it: accountId still selects the one account the overlays describe.
	// ErrNoRows is its "unknown account, no overlays" path, so nothing after it
	// runs.
	mock.ExpectQuery("SELECT a.billing_day").
		WithArgs(acctID, userID).
		WillReturnError(pgx.ErrNoRows)

	req, _ := http.NewRequest(http.MethodGet,
		"/dashboard/cash-flow-calendar?dateFrom="+dateFrom+"&dateTo="+dateTo+"&accountId="+acctID.String()+"&currency=inr", nil)
	w := httptest.NewRecorder()
	newCashFlowCalendarTestRouter(newTestServer(mock)).ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var cal models.CashFlowCalendar
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &cal))

	// The case is folded, so a lowercase code still finds the account rather than
	// binding against 'INR' and quietly reporting an empty calendar.
	assert.Equal(t, []string{"INR"}, cal.CurrencyScope.Currencies)
	require.Len(t, cal.Days, 1)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(50000)}, cal.Days[0].Net)
	assert.Empty(t, cal.Markers)
	assert.Empty(t, cal.Cycles)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetCashFlowCalendarAccountFilter(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	srv := newTestServer(mock)

	userID := testUserID()
	acctID := uuid.New()
	dateFrom := "2024-06-01"
	dateTo := "2024-06-30"

	// Account has no billing day: month-end running balance markers.
	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	mock.ExpectQuery(calendarScopeRegex).
		WithArgs(userID, dateFrom, dateTo, acctID.String()).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
			AddRow(acctID, "Savings", "INR", money.FromFloat(0), money.FromFloat(1500)))
	mock.ExpectQuery(calendarDailyHead + `[\s\S]*` + calendarDailyTail).
		WithArgs(userID, dateFrom, dateTo, acctID.String()).
		WillReturnRows(pgxmock.NewRows(cashFlowDayCols()).
			AddRow(time.Date(2024, 6, 3, 0, 0, 0, 0, time.UTC), "INR", money.FromFloat(0), money.FromFloat(1500), 2))
	mock.ExpectCommit()

	mock.ExpectQuery("SELECT a.billing_day").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"billing_day", "currency"}).AddRow(nil, "INR"))
	mock.ExpectQuery("date_trunc\\('month', t.date\\)").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"month", "net", "count"}).
			AddRow(time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC), -1500.0, 2))

	req, _ := http.NewRequest(http.MethodGet,
		"/dashboard/cash-flow-calendar?dateFrom="+dateFrom+"&dateTo="+dateTo+"&accountId="+acctID.String(), nil)
	w := httptest.NewRecorder()
	newCashFlowCalendarTestRouter(srv).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var cal models.CashFlowCalendar
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &cal))

	require.Len(t, cal.Markers, 1)
	assert.Equal(t, "2024-06-30", cal.Markers[0].Date)
	assert.Equal(t, "Running balance", cal.Markers[0].Label)
	assert.Equal(t, "balance", cal.Markers[0].Kind)
	// The overlay belongs to the one selected account, so its amount is a map
	// with that account's single key.
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(-1500)}, cal.Markers[0].Amount)
	if _, _, ok := cal.Markers[0].Amount.Single(); !ok {
		t.Error("a single-account marker amount did not report as a single amount")
	}
	assert.Empty(t, cal.Cycles)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetCashFlowCalendarBillingCycles(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	srv := newTestServer(mock)

	userID := testUserID()
	acctID := uuid.New()
	cycleID := uuid.New()
	dateFrom := "2024-05-06"
	dateTo := "2024-06-05"
	cycleStart := time.Date(2024, 5, 6, 0, 0, 0, 0, time.UTC)
	cycleEnd := time.Date(2024, 6, 5, 0, 0, 0, 0, time.UTC)

	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	mock.ExpectQuery(calendarScopeRegex).
		WithArgs(userID, dateFrom, dateTo, acctID.String()).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
			AddRow(acctID, "Card", "USD", money.FromFloat(0), money.FromFloat(500)))
	mock.ExpectQuery(calendarDailyHead + `[\s\S]*` + calendarDailyTail).
		WithArgs(userID, dateFrom, dateTo, acctID.String()).
		WillReturnRows(pgxmock.NewRows(cashFlowDayCols()).
			AddRow(cycleEnd, "USD", money.FromFloat(0), money.FromFloat(500), 3))
	mock.ExpectCommit()

	mock.ExpectQuery("SELECT a.billing_day").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"billing_day", "currency"}).AddRow(intPtr(5), "USD"))
	expectBillingCyclesUpToDate(mock, userID, acctID, 5)
	mock.ExpectQuery("SELECT bc.id, bc.start_date, bc.end_date, bc.label").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "start_date", "end_date", "label", "net_activity", "txn_count"}).
			AddRow(cycleID, cycleStart, cycleEnd, "Jun 2024", 500.0, 3))

	req, _ := http.NewRequest(http.MethodGet,
		"/dashboard/cash-flow-calendar?dateFrom="+dateFrom+"&dateTo="+dateTo+"&accountId="+acctID.String(), nil)
	w := httptest.NewRecorder()
	newCashFlowCalendarTestRouter(srv).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var cal models.CashFlowCalendar
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &cal))

	require.Len(t, cal.Cycles, 1)
	assert.Equal(t, cycleID, cal.Cycles[0].ID)
	assert.Equal(t, "Jun 2024", cal.Cycles[0].Label)
	// The cycle's outstanding balance is the one account's money, in that
	// account's own currency.
	assert.Equal(t, models.CurrencyAmounts{"USD": money.FromFloat(500)}, cal.Cycles[0].Outstanding)

	require.Len(t, cal.Markers, 1)
	assert.Equal(t, "2024-06-05", cal.Markers[0].Date)
	assert.Equal(t, "Total outstanding", cal.Markers[0].Label)
	assert.Equal(t, "outstanding", cal.Markers[0].Kind)
	assert.Equal(t, models.CurrencyAmounts{"USD": money.FromFloat(500)}, cal.Markers[0].Amount)

	// The whole response is in the one account's currency, so every amount here
	// is a one-key map and a client can read one out with Single.
	assert.Equal(t, models.CurrencyAmounts{"USD": money.FromFloat(500)}, cal.MaxAbsNet)
	assert.Equal(t, models.CurrencyAmounts{"USD": money.FromFloat(-500)}, cal.Net)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetCashFlowCalendarErrors(t *testing.T) {
	userID := testUserID()

	t.Run("invalid account id", func(t *testing.T) {
		srv := newTestServer(nil)
		w := httptest.NewRecorder()
		newCashFlowCalendarTestRouter(srv).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "/dashboard/cash-flow-calendar?accountId=not-a-uuid", nil))
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("begin error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}).
			WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		newCashFlowCalendarTestRouter(newTestServer(mock)).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "/dashboard/cash-flow-calendar", nil))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("currency scope error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		mock.ExpectQuery(calendarScopeRegex).
			WithArgs(userID).
			WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		newCashFlowCalendarTestRouter(newTestServer(mock)).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "/dashboard/cash-flow-calendar", nil))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("daily query error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		mock.ExpectQuery(calendarScopeRegex).
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}))
		mock.ExpectQuery(calendarDailyHead + `[\s\S]*` + calendarDailyTail).
			WithArgs(userID).
			WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		newCashFlowCalendarTestRouter(newTestServer(mock)).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "/dashboard/cash-flow-calendar", nil))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("overlay lookup error still returns the calendar", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		acctID := uuid.New()
		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		mock.ExpectQuery(calendarScopeRegex).
			WithArgs(userID, acctID.String()).
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}))
		mock.ExpectQuery(calendarDailyHead + `[\s\S]*` + calendarDailyTail).
			WithArgs(userID, acctID.String()).
			WillReturnRows(pgxmock.NewRows(cashFlowDayCols()))
		mock.ExpectCommit()
		mock.ExpectQuery("SELECT a.billing_day").
			WithArgs(acctID, userID).
			WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		newCashFlowCalendarTestRouter(newTestServer(mock)).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "/dashboard/cash-flow-calendar?accountId="+acctID.String(), nil))

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `"markers":[]`)
		// A response covering nothing still spells every collection out: an
		// empty day list and empty maps, never null. A nil map would marshal as
		// null and break the {} the contract promises.
		assert.Contains(t, w.Body.String(), `"days":[]`)
		assert.Contains(t, w.Body.String(), `"totalIncome":{}`)
		assert.Contains(t, w.Body.String(), `"net":{}`)
		assert.Contains(t, w.Body.String(), `"maxAbsNet":{}`)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// The date window is compared against a date column, so a malformed bound must
// be a 400 rather than a Postgres parse error surfacing as a 500.
func TestGetCashFlowCalendarRejectsMalformedDates(t *testing.T) {
	srv, _ := newMockServer(t)
	r := newCashFlowCalendarTestRouter(srv)

	for _, query := range []string{"dateFrom=2024-1-5", "dateTo=not-a-date"} {
		t.Run(query, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, "/dashboard/cash-flow-calendar?"+query, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), "must be YYYY-MM-DD")
		})
	}
}

// A currency that is not three letters cannot be compared against
// accounts.currency, and ignoring it silently would report a window the caller
// did not ask for.
func TestGetCashFlowCalendarRejectsInvalidCurrency(t *testing.T) {
	srv, _ := newMockServer(t)
	r := newCashFlowCalendarTestRouter(srv)

	for _, code := range []string{"US", "USDD", "12A"} {
		t.Run(code, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, "/dashboard/cash-flow-calendar?currency="+code, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), "invalid currency")
		})
	}
}
