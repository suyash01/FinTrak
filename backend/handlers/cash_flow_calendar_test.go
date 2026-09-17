package handlers

import (
	"encoding/json"
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

func cashFlowDayCols() []string {
	return []string{"date", "income", "expense", "count"}
}

func TestGetCashFlowCalendar(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	srv := newTestServer(mock)

	userID := testUserID()
	dateFrom := "2024-06-01"
	dateTo := "2024-06-30"

	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	mock.ExpectQuery("GROUP BY t.date").
		WithArgs(userID, dateFrom, dateTo).
		WillReturnRows(pgxmock.NewRows(cashFlowDayCols()).
			AddRow(time.Date(2024, 6, 2, 0, 0, 0, 0, time.UTC), 50000.0, 0.0, 1).
			AddRow(time.Date(2024, 6, 3, 0, 0, 0, 0, time.UTC), 0.0, 1500.0, 2))
	mock.ExpectCommit()

	req, _ := http.NewRequest(http.MethodGet, "/dashboard/cash-flow-calendar?dateFrom="+dateFrom+"&dateTo="+dateTo, nil)
	w := httptest.NewRecorder()
	newCashFlowCalendarTestRouter(srv).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var cal models.CashFlowCalendar
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &cal))

	require.Len(t, cal.Days, 2)
	assert.Equal(t, "2024-06-02", cal.Days[0].Date)
	assert.Equal(t, money.FromFloat(50000), cal.Days[0].Income)
	assert.Equal(t, money.Amount(0), cal.Days[0].Expense)
	assert.Equal(t, money.FromFloat(50000), cal.Days[0].Net)
	assert.Equal(t, 1, cal.Days[0].Count)

	assert.Equal(t, "2024-06-03", cal.Days[1].Date)
	assert.Equal(t, money.FromFloat(-1500), cal.Days[1].Net)

	assert.Equal(t, money.FromFloat(50000), cal.TotalIncome)
	assert.Equal(t, money.FromFloat(1500), cal.TotalExpense)
	assert.Equal(t, money.FromFloat(48500), cal.Net)
	assert.Equal(t, money.FromFloat(50000), cal.MaxAbsNet)
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
	mock.ExpectQuery("GROUP BY t.date").
		WithArgs(userID, dateFrom, dateTo, acctID.String()).
		WillReturnRows(pgxmock.NewRows(cashFlowDayCols()).
			AddRow(time.Date(2024, 6, 3, 0, 0, 0, 0, time.UTC), 0.0, 1500.0, 2))
	mock.ExpectCommit()

	mock.ExpectQuery("SELECT a.billing_day").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"billing_day"}).AddRow(nil))
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
	assert.Equal(t, money.FromFloat(-1500), cal.Markers[0].Amount)
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
	mock.ExpectQuery("GROUP BY t.date").
		WithArgs(userID, dateFrom, dateTo, acctID.String()).
		WillReturnRows(pgxmock.NewRows(cashFlowDayCols()).
			AddRow(cycleEnd, 0.0, 500.0, 3))
	mock.ExpectCommit()

	mock.ExpectQuery("SELECT a.billing_day").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"billing_day"}).AddRow(intPtr(5)))
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
	assert.Equal(t, money.FromFloat(500), cal.Cycles[0].Outstanding)

	require.Len(t, cal.Markers, 1)
	assert.Equal(t, "2024-06-05", cal.Markers[0].Date)
	assert.Equal(t, "Total outstanding", cal.Markers[0].Label)
	assert.Equal(t, "outstanding", cal.Markers[0].Kind)
	assert.Equal(t, money.FromFloat(500), cal.Markers[0].Amount)

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

	t.Run("daily query error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		mock.ExpectQuery("GROUP BY t.date").
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
		mock.ExpectQuery("GROUP BY t.date").
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
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
