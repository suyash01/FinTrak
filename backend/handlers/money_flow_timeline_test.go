package handlers

import (
	"context"
	"encoding/json"
	"errors"
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

func newMoneyFlowTimelineTestRouter(srv *Server) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.GET("/dashboard/money-flow/timeline", srv.GetMoneyFlowTimeline)
	return r
}

// The account-currency expression, as it has to appear in the SELECT projection
// and in the ?currency= predicate of every query that reads it - and, through the
// ordinal GROUP BY, in the grouping. The expectations match these rather than a
// loose "a.currency" because pgxmock compares arguments with reflect.DeepEqual: a
// respelled expression binds identically, so it would sail through a loose regexp
// while disagreeing with the scope query the same response's currencyScope was
// folded from - and would drop every account whose currency is merely unset out of
// an explicit ?currency=INR report.
const (
	timelineCurrencyRegex     = `COALESCE\(NULLIF\(a\.currency, ''\), 'INR'\)`
	timelineCurrencyProjRegex = timelineCurrencyRegex + ` AS currency`
	// The shared filter binds the window first and the currency last, so with a
	// window and no account the predicate lands on $4 - which is also where the
	// scope query reaches, since it numbers the same filters in the same order.
	timelineCurrencyPredRegex = timelineCurrencyRegex + ` = \$4`
	// timelineMonthlyHead pins the currency as the *second* select expression,
	// because the statement groups by ordinal: `GROUP BY 1, 2` means the month
	// and the currency in that order, and a currency projected ahead of the month
	// would silently group the wrong column.
	timelineMonthlyHead = `(?s)SELECT date_trunc\('month', t\.date\)::date, ` + timelineCurrencyProjRegex
	timelineMonthlyTail = `GROUP BY 1, 2`
	// The scope query is the shared currencyScope statement, which puts the
	// window in the join's ON so a quiet account still names its currency.
	timelineScopeRegex = `FROM accounts a\s+LEFT JOIN transactions t`
	// timelineCycleJoin pins the billing-cycle periods query to the date-range
	// join, because a substring that merely starts at "FROM billing_cycles bc"
	// is satisfied by both joins and cannot see the difference between them. The
	// join decides which transactions a period holds, and the scope beside it is
	// read by date: join by cycle id instead and a detached transaction is in the
	// scope's totals and in no period, so the response states two figures for one
	// window with nothing reconciling them. The user_id guard on the transactions
	// join is pinned with it for the same reason - a date range spanning accounts
	// would fold another user's money into this account's periods.
	timelineCycleJoinRegex = `LEFT JOIN transactions t ON t\.account_id = bc\.account_id ` +
		`AND t\.user_id = \$2\s+AND t\.date >= bc\.start_date AND t\.date <= bc\.end_date`
)

// expectTimelineAccountCurrency expects the in-snapshot read of the account's
// currency, the one figure that keys every period. It is a separate query from
// the billing-day lookup, and it happens after the read-only transaction has
// begun, because it has to share that transaction with the currencyScope it must
// agree with: a currency read on the pool would let a concurrent edit key the
// periods by a currency the in-snapshot scope does not name. pgxmock matches
// expectations in order, so placing this call before the scope expectation is
// what holds the handler to that order.
func expectTimelineAccountCurrency(mock pgxmock.PgxPoolIface, acctID, userID uuid.UUID, code string) {
	mock.ExpectQuery("SELECT " + timelineCurrencyRegex + `\s+FROM accounts a`).
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"currency"}).AddRow(code))
}

func TestGetMoneyFlowTimelineMonthly(t *testing.T) {
	mock, err := newGuardedPool(t)
	require.NoError(t, err)
	defer mock.Close()

	userID := testUserID()
	acctID := uuid.New()
	dateFrom, dateTo := "2024-05-01", "2024-06-30"

	// The scope and the periods are read in one snapshot, so the two can never
	// describe different ledgers; the begin and commit are what pin that, and
	// dropping either fails here.
	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	mock.ExpectQuery(timelineScopeRegex).
		WithArgs(userID, dateFrom, dateTo).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
			AddRow(acctID, "Salary", "INR", money.FromFloat(50000), money.FromFloat(15000)))
	mock.ExpectQuery(timelineMonthlyHead + `[\s\S]*` + timelineMonthlyTail).
		WithArgs(userID, dateFrom, dateTo).
		WillReturnRows(pgxmock.NewRows([]string{"month", "currency", "income", "expense"}).
			AddRow(time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC), "INR", money.FromFloat(50000), money.FromFloat(12000)).
			AddRow(time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC), "INR", 0, money.FromFloat(3000)))
	mock.ExpectCommit()

	req, _ := http.NewRequest(http.MethodGet,
		"/dashboard/money-flow/timeline?dateFrom="+dateFrom+"&dateTo="+dateTo, nil)
	w := httptest.NewRecorder()
	newMoneyFlowTimelineTestRouter(newTestServer(mock)).ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var timeline models.MoneyFlowTimeline
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &timeline))
	assert.Equal(t, "month", timeline.GroupBy)
	require.Len(t, timeline.Periods, 2)

	assert.Equal(t, "2024-05", timeline.Periods[0].Key)
	assert.Equal(t, "May 2024", timeline.Periods[0].Label)
	assert.Equal(t, "2024-05-01", timeline.Periods[0].StartDate)
	assert.Equal(t, "2024-05-31", timeline.Periods[0].EndDate)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(50000)}, timeline.Periods[0].Income)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(12000)}, timeline.Periods[0].Expense)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(38000)}, timeline.Periods[0].Net)
	if _, _, ok := timeline.Periods[0].Net.Single(); !ok {
		t.Error("a single-currency period net did not report as a single amount")
	}

	// June spent without earning, so its income map is empty rather than
	// present-and-zero and its net is the negative of its expense.
	assert.Equal(t, models.CurrencyAmounts{}, timeline.Periods[1].Income)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(-3000)}, timeline.Periods[1].Net)

	assert.Equal(t, []string{"INR"}, timeline.CurrencyScope.Currencies)
	require.Len(t, timeline.CurrencyScope.Accounts, 1)
	assert.Equal(t, "Salary", timeline.CurrencyScope.Accounts[0].Name)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// The bug this endpoint had: with no account filter, a month covering a USD
// account and an INR one added dollars to rupees. A difference within one
// currency is still meaningful, so the net survives - per currency, and with
// nothing anywhere in the response taken across the two.
func TestGetMoneyFlowTimelineRefusesACrossCurrencyNet(t *testing.T) {
	mock, err := newGuardedPool(t)
	require.NoError(t, err)
	defer mock.Close()

	userID := testUserID()
	acctINR, acctUSD := uuid.New(), uuid.New()
	dateFrom, dateTo := "2024-05-01", "2024-06-30"
	may := time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)

	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	mock.ExpectQuery(timelineScopeRegex).
		WithArgs(userID, dateFrom, dateTo).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
			AddRow(acctINR, "Salary", "INR", money.FromFloat(50000), money.FromFloat(12000)).
			AddRow(acctUSD, "Travel card", "USD", money.FromFloat(200), money.FromFloat(50)))
	mock.ExpectQuery(timelineMonthlyHead + `[\s\S]*` + timelineMonthlyTail).
		WithArgs(userID, dateFrom, dateTo).
		WillReturnRows(pgxmock.NewRows([]string{"month", "currency", "income", "expense"}).
			AddRow(may, "INR", money.FromFloat(50000), money.FromFloat(12000)).
			AddRow(may, "USD", money.FromFloat(200), money.FromFloat(50)))
	mock.ExpectCommit()

	req, _ := http.NewRequest(http.MethodGet,
		"/dashboard/money-flow/timeline?dateFrom="+dateFrom+"&dateTo="+dateTo, nil)
	w := httptest.NewRecorder()
	newMoneyFlowTimelineTestRouter(newTestServer(mock)).ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var timeline models.MoneyFlowTimeline
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &timeline))

	// One month, one period: the currency is part of the amount, not of the
	// period's identity. Grouping the month without the currency let the
	// database hand back one amount that had already added dollars to rupees,
	// before any map existed to keep them apart.
	require.Len(t, timeline.Periods, 1)
	assert.Equal(t, models.CurrencyAmounts{
		"INR": money.FromFloat(50000),
		"USD": money.FromFloat(200),
	}, timeline.Periods[0].Income)
	assert.Equal(t, models.CurrencyAmounts{
		"INR": money.FromFloat(38000),
		"USD": money.FromFloat(150),
	}, timeline.Periods[0].Net)
	if _, _, ok := timeline.Periods[0].Net.Single(); ok {
		t.Error("a two-currency period net reported as a single amount")
	}

	// The scope names both accounts, so a reader can see that USD 150.00 exists
	// rather than inferring a single number from the sum of the two keys.
	assert.Equal(t, []string{"INR", "USD"}, timeline.CurrencyScope.Currencies)
	require.Len(t, timeline.CurrencyScope.Accounts, 2)
	assert.Equal(t, "INR", timeline.CurrencyScope.Accounts[0].Currency)
	assert.Equal(t, "USD", timeline.CurrencyScope.Accounts[1].Currency)

	// The net reaches the wire as an object keyed by currency. A bare number
	// here would be the cross-currency total this change exists to remove.
	assert.Contains(t, w.Body.String(), `"net":{"INR":38000.00,"USD":150.00}`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// The projected-currency expression has to be the same one the scope query
// projects, filters and groups by, and the ?currency= predicate has to be the
// same one the projection renders - at every one of the three sites, in one
// expectation. The bound argument alone cannot catch a respelling: pgxmock
// compares arguments with reflect.DeepEqual, so a bare COALESCE(a.currency, ...)
// in the projection would bind the same "USD" and pass every loose expectation.
func TestGetMoneyFlowTimelinePinsTheProjectedCurrency(t *testing.T) {
	mock, err := newGuardedPool(t)
	require.NoError(t, err)
	defer mock.Close()

	userID := testUserID()
	acctID := uuid.New()
	dateFrom, dateTo := "2024-05-01", "2024-06-30"

	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	mock.ExpectQuery(timelineScopeRegex + `[\s\S]*` + timelineCurrencyPredRegex).
		WithArgs(userID, dateFrom, dateTo, "USD").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
			AddRow(acctID, "Travel card", "USD", money.FromFloat(200), money.FromFloat(50)))
	mock.ExpectQuery(timelineMonthlyHead +
		`[\s\S]*t\.user_id = \$1 AND t\.date >= \$2 AND t\.date <= \$3 AND ` + timelineCurrencyPredRegex +
		`[\s\S]*` + timelineMonthlyTail).
		WithArgs(userID, dateFrom, dateTo, "USD").
		WillReturnRows(pgxmock.NewRows([]string{"month", "currency", "income", "expense"}).
			AddRow(time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC), "USD", money.FromFloat(200), money.FromFloat(50)))
	mock.ExpectCommit()

	req, _ := http.NewRequest(http.MethodGet,
		"/dashboard/money-flow/timeline?dateFrom="+dateFrom+"&dateTo="+dateTo+"&currency=usd", nil)
	w := httptest.NewRecorder()
	newMoneyFlowTimelineTestRouter(newTestServer(mock)).ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var timeline models.MoneyFlowTimeline
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &timeline))
	// The filter was folded to upper case by the shared parser, so ?currency=usd
	// finds the USD account rather than matching nothing and reporting zeroes.
	assert.Equal(t, []string{"USD"}, timeline.CurrencyScope.Currencies)
	assert.Equal(t, models.CurrencyAmounts{"USD": money.FromFloat(150)}, timeline.Periods[0].Net)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// The currency predicate sits at $4 in every other expectation in this file,
// because those requests carry no account. With accountId present too,
// flowFilter's order - window, then account, then currency - puts it at $5, and
// the scope query reaches $5 for the same three filters. Nothing else here covers
// that combination, and a mismatch is a wrong binding rather than a wrong number:
// pgxmock compares the arguments, so pinning the placeholder is the only thing
// that catches it.
func TestGetMoneyFlowTimelineMonthlyWithAccountAndCurrency(t *testing.T) {
	mock, err := newGuardedPool(t)
	require.NoError(t, err)
	defer mock.Close()

	userID := testUserID()
	acctID := uuid.New()
	dateFrom, dateTo := "2024-05-01", "2024-06-30"
	const predAt5 = ` = \$5`

	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	mock.ExpectQuery(timelineScopeRegex + `[\s\S]*AND a\.id = \$4 AND ` + timelineCurrencyRegex + predAt5).
		WithArgs(userID, dateFrom, dateTo, acctID.String(), "USD").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
			AddRow(acctID, "Travel card", "USD", money.FromFloat(200), money.FromFloat(50)))
	mock.ExpectQuery(timelineMonthlyHead +
		`[\s\S]*t\.date >= \$2 AND t\.date <= \$3 AND a\.id = \$4 AND ` + timelineCurrencyRegex + predAt5 +
		`[\s\S]*` + timelineMonthlyTail).
		WithArgs(userID, dateFrom, dateTo, acctID.String(), "USD").
		WillReturnRows(pgxmock.NewRows([]string{"month", "currency", "income", "expense"}).
			AddRow(time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC), "USD", money.FromFloat(200), money.FromFloat(50)))
	mock.ExpectCommit()

	req, _ := http.NewRequest(http.MethodGet,
		"/dashboard/money-flow/timeline?dateFrom="+dateFrom+"&dateTo="+dateTo+
			"&accountId="+acctID.String()+"&currency=USD", nil)
	w := httptest.NewRecorder()
	newMoneyFlowTimelineTestRouter(newTestServer(mock)).ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var timeline models.MoneyFlowTimeline
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &timeline))
	assert.Equal(t, []string{"USD"}, timeline.CurrencyScope.Currencies)
	assert.Equal(t, models.CurrencyAmounts{"USD": money.FromFloat(150)}, timeline.Periods[0].Net)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetMoneyFlowTimelineBillingCycles(t *testing.T) {
	mock, err := newGuardedPool(t)
	require.NoError(t, err)
	defer mock.Close()

	userID := testUserID()
	acctID := uuid.New()
	cycleID := uuid.New()
	start := time.Date(2024, 5, 6, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 6, 5, 0, 0, 0, 0, time.UTC)

	// The lookup reads only the billing day, because ensureBillingCycles needs it
	// and runs before the snapshot. The currency comes with the scope below.
	mock.ExpectQuery("SELECT a.billing_day").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"billing_day"}).AddRow(intPtr(5)))
	expectBillingCyclesUpToDate(mock, userID, acctID, 5)
	mock.ExpectQuery("SELECT bc.id, bc.start_date, bc.end_date, bc.label").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "start_date", "end_date", "label", "net_activity", "txn_count"}).
			AddRow(cycleID, start, end, "Jun 2024", 500.0, 3))
	// The scope covers the same cycle window the periods are read over, so the
	// currency named beside the bars is the one the bars are in, and the two come
	// from one snapshot. So does the currency that keys the bars.
	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	// The branch is account-scoped, so one cycle - and one period - can only hold
	// the account's own currency.
	expectTimelineAccountCurrency(mock, acctID, userID, "INR")
	mock.ExpectQuery(timelineScopeRegex).
		WithArgs(userID, start.Format("2006-01-02"), end.Format("2006-01-02"), acctID.String()).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
			AddRow(acctID, "Savings", "INR", money.FromFloat(5000), money.FromFloat(1500)))
	// A cycle with no transactions still yields a period, and its maps are empty
	// objects rather than nulls.
	cycleRows := pgxmock.NewRows([]string{"id", "start_date", "end_date", "label", "income", "expense"}).
		AddRow(cycleID, start, end, "Jun 2024", money.FromFloat(5000), money.FromFloat(1500)).
		AddRow(uuid.New(), end.AddDate(0, 0, 1), end.AddDate(0, 1, 0), "Jul 2024", 0, 0)
	mock.ExpectQuery("FROM billing_cycles bc[\\s\\S]*" + timelineCycleJoinRegex).
		WithArgs(acctID, userID, start.Format("2006-01-02"), end.Format("2006-01-02")).
		WillReturnRows(cycleRows)
	mock.ExpectCommit()

	req, _ := http.NewRequest(http.MethodGet,
		"/dashboard/money-flow/timeline?groupBy=billing_cycle&accountId="+acctID.String(), nil)
	w := httptest.NewRecorder()
	newMoneyFlowTimelineTestRouter(newTestServer(mock)).ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var timeline models.MoneyFlowTimeline
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &timeline))
	assert.Equal(t, "billing_cycle", timeline.GroupBy)
	require.Len(t, timeline.Periods, 2)
	assert.Equal(t, cycleID.String(), timeline.Periods[0].Key)
	assert.Equal(t, "Jun 2024", timeline.Periods[0].Label)
	assert.Equal(t, "2024-05-06", timeline.Periods[0].StartDate)
	assert.Equal(t, "2024-06-05", timeline.Periods[0].EndDate)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(5000)}, timeline.Periods[0].Income)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(1500)}, timeline.Periods[0].Expense)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(3500)}, timeline.Periods[0].Net)
	// The branch is account-scoped, so the net is still readable as one number.
	if _, _, ok := timeline.Periods[0].Net.Single(); !ok {
		t.Error("an account-scoped period net did not report as a single amount")
	}
	assert.Equal(t, models.CurrencyAmounts{}, timeline.Periods[1].Income)
	assert.Equal(t, models.CurrencyAmounts{}, timeline.Periods[1].Net)
	assert.Equal(t, []string{"INR"}, timeline.CurrencyScope.Currencies)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// The billing-cycle branch is one account, so it needs no per-currency grouping
// - but it must still narrow on ?currency= rather than ignore it, and its cycle
// query numbers its own arguments (account, user, window, window, currency) so
// the predicate sits at a fixed fifth placeholder. Nothing but a test would notice
// if that placeholder and the argument appended after it drifted apart.
func TestGetMoneyFlowTimelineBillingCycleNarrowsToTheCurrencyFilter(t *testing.T) {
	mock, err := newGuardedPool(t)
	require.NoError(t, err)
	defer mock.Close()

	userID := testUserID()
	acctID := uuid.New()
	cycleID := uuid.New()
	start := time.Date(2024, 5, 6, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 6, 5, 0, 0, 0, 0, time.UTC)
	windowStart, windowEnd := start.Format("2006-01-02"), end.Format("2006-01-02")
	const predAt5 = ` = \$5`

	mock.ExpectQuery("SELECT a.billing_day").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"billing_day"}).AddRow(intPtr(5)))
	expectBillingCyclesUpToDate(mock, userID, acctID, 5)
	mock.ExpectQuery("SELECT bc.id, bc.start_date, bc.end_date, bc.label").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "start_date", "end_date", "label", "net_activity", "txn_count"}).
			AddRow(cycleID, start, end, "Jun 2024", 3500.0, 3))
	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	expectTimelineAccountCurrency(mock, acctID, userID, "INR")
	mock.ExpectQuery(timelineScopeRegex + `[\s\S]*` + timelineCurrencyRegex + predAt5).
		WithArgs(userID, windowStart, windowEnd, acctID.String(), "INR").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
			AddRow(acctID, "Savings", "INR", money.FromFloat(5000), money.FromFloat(1500)))
	mock.ExpectQuery("FROM billing_cycles bc[\\s\\S]*JOIN accounts a ON a\\.id = bc\\.account_id[\\s\\S]*" +
		timelineCycleJoinRegex + "[\\s\\S]*" + timelineCurrencyRegex + predAt5).
		WithArgs(acctID, userID, windowStart, windowEnd, "INR").
		WillReturnRows(pgxmock.NewRows([]string{"id", "start_date", "end_date", "label", "income", "expense"}).
			AddRow(cycleID, start, end, "Jun 2024", money.FromFloat(5000), money.FromFloat(1500)))
	mock.ExpectCommit()

	req, _ := http.NewRequest(http.MethodGet,
		"/dashboard/money-flow/timeline?groupBy=billing_cycle&accountId="+acctID.String()+"&currency=INR", nil)
	w := httptest.NewRecorder()
	newMoneyFlowTimelineTestRouter(newTestServer(mock)).ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var timeline models.MoneyFlowTimeline
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &timeline))
	assert.Equal(t, []string{"INR"}, timeline.CurrencyScope.Currencies)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(3500)}, timeline.Periods[0].Net)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A ?currency= the account does not hold empties both halves of the response:
// the periods query filters the cycles by the account's own currency, and the
// scope query reports no account. This is a decision rather than an accident -
// the alternative would be to report the account's INR periods beside a scope
// that names no currency, or a period list for a currency nobody asked for. It
// answers 200 with an empty list, and the empty currencyScope is what
// distinguishes it from an account with no statement periods at all.
func TestGetMoneyFlowTimelineBillingCycleCurrencyMismatchIsEmpty(t *testing.T) {
	mock, err := newGuardedPool(t)
	require.NoError(t, err)
	defer mock.Close()

	userID := testUserID()
	acctID := uuid.New()
	cycleID := uuid.New()
	start := time.Date(2024, 5, 6, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 6, 5, 0, 0, 0, 0, time.UTC)
	windowStart, windowEnd := start.Format("2006-01-02"), end.Format("2006-01-02")

	// A USD account asked for INR. The cycles exist - the second query below is
	// the one that filters them out.
	mock.ExpectQuery("SELECT a.billing_day").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"billing_day"}).AddRow(intPtr(5)))
	expectBillingCyclesUpToDate(mock, userID, acctID, 5)
	mock.ExpectQuery("SELECT bc.id, bc.start_date, bc.end_date, bc.label").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "start_date", "end_date", "label", "net_activity", "txn_count"}).
			AddRow(cycleID, start, end, "Jun 2024", 3500.0, 3))
	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	// The account is USD, and it is read from inside the snapshot even though the
	// periods come back empty: the read happens before the scope, so a response
	// with no periods still cost it. That is the price of the two agreeing, and
	// it is worth paying on the empty path too - a read that only happens when
	// there is something to say is a read that can disagree when there is not.
	expectTimelineAccountCurrency(mock, acctID, userID, "USD")
	// The scope's own currency predicate is what makes the account disappear;
	// without it the response would name a USD account inside a ?currency=INR
	// report.
	mock.ExpectQuery(timelineScopeRegex + `[\s\S]*` + timelineCurrencyRegex + ` = \$5`).
		WithArgs(userID, windowStart, windowEnd, acctID.String(), "INR").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}))
	mock.ExpectQuery("FROM billing_cycles bc[\\s\\S]*" + timelineCycleJoinRegex +
		"[\\s\\S]*" + timelineCurrencyRegex + ` = \$5`).
		WithArgs(acctID, userID, windowStart, windowEnd, "INR").
		WillReturnRows(pgxmock.NewRows([]string{"id", "start_date", "end_date", "label", "income", "expense"}))
	mock.ExpectCommit()

	req, _ := http.NewRequest(http.MethodGet,
		"/dashboard/money-flow/timeline?groupBy=billing_cycle&accountId="+acctID.String()+"&currency=INR", nil)
	w := httptest.NewRecorder()
	newMoneyFlowTimelineTestRouter(newTestServer(mock)).ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t,
		`{"groupBy":"billing_cycle","periods":[],"currencyScope":{"currencies":[],"accounts":[]}}`,
		w.Body.String())
	assert.NoError(t, mock.ExpectationsWereMet())
}

// An account with no statement periods returns before the scope query runs, so
// this is the one response whose currencyScope is built here rather than folded.
// It is still an object with two empty arrays: a null there would tell a client
// the currencies are unknown where the truth is that there are none.
func TestGetMoneyFlowTimelineBillingCycleNoCycles(t *testing.T) {
	mock, err := newGuardedPool(t)
	require.NoError(t, err)
	defer mock.Close()

	userID := testUserID()
	acctID := uuid.New()

	// No cycles, so the handler returns before the snapshot opens and the
	// currency is never read - there is no period to key.
	mock.ExpectQuery("SELECT a.billing_day").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"billing_day"}).AddRow(intPtr(5)))
	expectBillingCyclesUpToDate(mock, userID, acctID, 5)
	mock.ExpectQuery("SELECT bc.id, bc.start_date, bc.end_date, bc.label").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "start_date", "end_date", "label", "net_activity", "txn_count"}))

	req, _ := http.NewRequest(http.MethodGet,
		"/dashboard/money-flow/timeline?groupBy=billing_cycle&accountId="+acctID.String(), nil)
	w := httptest.NewRecorder()
	newMoneyFlowTimelineTestRouter(newTestServer(mock)).ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"groupBy":"billing_cycle","periods":[],"currencyScope":{"currencies":[],"accounts":[]}}`, w.Body.String())
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Both folds run inside a transaction the handler also commits, so a row-stream
// failure and a commit failure are the same 500 from the router: a test at the
// router could not tell which branch answered, and a fold that dropped its
// rows.Err() check would pass it. These two call the folds directly instead.
//
// A stream that fails *after* a row is exactly the case worth pinning: the fold
// has a period in hand, and returning it would answer 200 with a strip quietly
// missing its later periods - read as a quiet month or a quiet cycle rather than
// as a failed read.
func TestTimelineFoldsPropagateARowError(t *testing.T) {
	boom := errors.New("connection reset mid-fold")
	month := time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)
	cycleStart := time.Date(2024, 5, 6, 0, 0, 0, 0, time.UTC)
	cycleEnd := time.Date(2024, 6, 5, 0, 0, 0, 0, time.UTC)

	t.Run("monthly", func(t *testing.T) {
		mock, err := newGuardedPool(t)
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectQuery(timelineMonthlyHead).
			WithArgs(testUserID()).
			WillReturnRows(pgxmock.NewRows([]string{"month", "currency", "income", "expense"}).
				AddRow(month, "INR", money.FromFloat(50000), money.FromFloat(12000)).
				RowError(1, boom))

		periods, err := queryMonthlyFlowTimeline(context.Background(), mock, testUserID(), "", "", "", "")
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want %v", err, boom)
		}
		if len(periods) != 0 {
			t.Errorf("Periods = %v on a row error, want none", periods)
		}
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("billing cycle", func(t *testing.T) {
		rows := pgxmock.NewRows([]string{"id", "start_date", "end_date", "label", "income", "expense"}).
			AddRow(uuid.New(), cycleStart, cycleEnd, "Jun 2024", money.FromFloat(5000), money.FromFloat(1500)).
			RowError(1, boom)

		periods, err := billingCycleTimelinePeriods(rows.Kind(), "INR")
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want %v", err, boom)
		}
		if len(periods) != 0 {
			t.Errorf("Periods = %v on a row error, want none", periods)
		}
	})
}

func TestGetMoneyFlowTimelineErrors(t *testing.T) {
	userID := testUserID()

	t.Run("invalid account id", func(t *testing.T) {
		w := httptest.NewRecorder()
		newMoneyFlowTimelineTestRouter(newTestServer(nil)).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "/dashboard/money-flow/timeline?accountId=nope", nil))
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("begin error", func(t *testing.T) {
		mock, err := newGuardedPool(t)
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}).
			WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		newMoneyFlowTimelineTestRouter(newTestServer(mock)).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "/dashboard/money-flow/timeline", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("monthly query error", func(t *testing.T) {
		mock, err := newGuardedPool(t)
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		mock.ExpectQuery(timelineScopeRegex).
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}))
		mock.ExpectQuery(timelineMonthlyHead).
			WithArgs(userID).
			WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		newMoneyFlowTimelineTestRouter(newTestServer(mock)).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "/dashboard/money-flow/timeline", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("currency scope error", func(t *testing.T) {
		mock, err := newGuardedPool(t)
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		mock.ExpectQuery(timelineScopeRegex).
			WithArgs(userID).
			WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		newMoneyFlowTimelineTestRouter(newTestServer(mock)).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "/dashboard/money-flow/timeline", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// A failed commit means the snapshot was not the one the response describes,
	// so the response is withheld rather than sent from a transaction Postgres
	// has already discarded.
	t.Run("commit error", func(t *testing.T) {
		mock, err := newGuardedPool(t)
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		mock.ExpectQuery(timelineScopeRegex).
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}))
		mock.ExpectQuery(timelineMonthlyHead).
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"month", "currency", "income", "expense"}))
		mock.ExpectCommit().WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		newMoneyFlowTimelineTestRouter(newTestServer(mock)).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "/dashboard/money-flow/timeline", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("invalid currency", func(t *testing.T) {
		w := httptest.NewRecorder()
		newMoneyFlowTimelineTestRouter(newTestServer(nil)).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "/dashboard/money-flow/timeline?currency=US", nil))
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("billing cycle requires account id", func(t *testing.T) {
		w := httptest.NewRecorder()
		newMoneyFlowTimelineTestRouter(newTestServer(nil)).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "/dashboard/money-flow/timeline?groupBy=billing_cycle", nil))
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("billing cycle without billing day", func(t *testing.T) {
		mock, err := newGuardedPool(t)
		require.NoError(t, err)
		defer mock.Close()

		acctID := uuid.New()
		mock.ExpectQuery("SELECT a.billing_day").
			WithArgs(acctID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"billing_day"}).AddRow(nil))

		w := httptest.NewRecorder()
		newMoneyFlowTimelineTestRouter(newTestServer(mock)).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "/dashboard/money-flow/timeline?groupBy=billing_cycle&accountId="+acctID.String(), nil))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("account not found", func(t *testing.T) {
		mock, err := newGuardedPool(t)
		require.NoError(t, err)
		defer mock.Close()

		acctID := uuid.New()
		mock.ExpectQuery("SELECT a.billing_day").
			WithArgs(acctID, userID).
			WillReturnError(pgx.ErrNoRows)

		w := httptest.NewRecorder()
		newMoneyFlowTimelineTestRouter(newTestServer(mock)).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "/dashboard/money-flow/timeline?groupBy=billing_cycle&accountId="+acctID.String(), nil))
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// The account's currency is read in the snapshot rather than with the billing
	// day, which opens a window the first read did not have: the account can be
	// deleted between them. Answering 404 is the honest response - the periods
	// and the scope below have nothing left to describe, and inventing a currency
	// to key them with is the one thing this branch must never do.
	t.Run("account deleted between the two reads", func(t *testing.T) {
		mock, err := newGuardedPool(t)
		require.NoError(t, err)
		defer mock.Close()

		acctID := uuid.New()
		mock.ExpectQuery("SELECT a.billing_day").
			WithArgs(acctID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"billing_day"}).AddRow(intPtr(5)))
		expectBillingCyclesUpToDate(mock, userID, acctID, 5)
		mock.ExpectQuery("SELECT bc.id, bc.start_date, bc.end_date, bc.label").
			WithArgs(acctID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"id", "start_date", "end_date", "label", "net_activity", "txn_count"}).
				AddRow(uuid.New(),
					time.Date(2024, 5, 6, 0, 0, 0, 0, time.UTC),
					time.Date(2024, 6, 5, 0, 0, 0, 0, time.UTC), "Jun 2024", 500.0, 3))
		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		mock.ExpectQuery("SELECT " + timelineCurrencyRegex + `\s+FROM accounts a`).
			WithArgs(acctID, userID).
			WillReturnError(pgx.ErrNoRows)

		w := httptest.NewRecorder()
		newMoneyFlowTimelineTestRouter(newTestServer(mock)).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "/dashboard/money-flow/timeline?groupBy=billing_cycle&accountId="+acctID.String(), nil))
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("account currency read fails", func(t *testing.T) {
		mock, err := newGuardedPool(t)
		require.NoError(t, err)
		defer mock.Close()

		acctID := uuid.New()
		mock.ExpectQuery("SELECT a.billing_day").
			WithArgs(acctID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"billing_day"}).AddRow(intPtr(5)))
		expectBillingCyclesUpToDate(mock, userID, acctID, 5)
		mock.ExpectQuery("SELECT bc.id, bc.start_date, bc.end_date, bc.label").
			WithArgs(acctID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"id", "start_date", "end_date", "label", "net_activity", "txn_count"}).
				AddRow(uuid.New(),
					time.Date(2024, 5, 6, 0, 0, 0, 0, time.UTC),
					time.Date(2024, 6, 5, 0, 0, 0, 0, time.UTC), "Jun 2024", 500.0, 3))
		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		mock.ExpectQuery("SELECT " + timelineCurrencyRegex + `\s+FROM accounts a`).
			WithArgs(acctID, userID).
			WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		newMoneyFlowTimelineTestRouter(newTestServer(mock)).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "/dashboard/money-flow/timeline?groupBy=billing_cycle&accountId="+acctID.String(), nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// The date window is compared against a date column, so a malformed bound must
// be a 400 rather than a Postgres parse error surfacing as a 500.
func TestGetMoneyFlowTimelineRejectsMalformedDates(t *testing.T) {
	srv, _ := newMockServer(t)
	r := newMoneyFlowTimelineTestRouter(srv)

	for _, query := range []string{"dateFrom=2024-1-5", "dateTo=not-a-date"} {
		t.Run(query, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, "/dashboard/money-flow/timeline?"+query, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), "must be YYYY-MM-DD")
		})
	}
}
