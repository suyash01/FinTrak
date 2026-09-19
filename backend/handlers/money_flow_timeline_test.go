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

func newMoneyFlowTimelineTestRouter(srv *Server) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.GET("/dashboard/money-flow/timeline", srv.GetMoneyFlowTimeline)
	return r
}

func TestGetMoneyFlowTimelineMonthly(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	userID := testUserID()
	dateFrom, dateTo := "2024-05-01", "2024-06-30"

	mock.ExpectQuery("date_trunc\\('month', t.date\\)").
		WithArgs(userID, dateFrom, dateTo).
		WillReturnRows(pgxmock.NewRows([]string{"month", "income", "expense"}).
			AddRow(time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC), 50000.0, 12000.0).
			AddRow(time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC), 0.0, 3000.0))

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
	assert.Equal(t, money.FromFloat(50000), timeline.Periods[0].Income)
	assert.Equal(t, money.FromFloat(12000), timeline.Periods[0].Expense)
	assert.Equal(t, money.FromFloat(38000), timeline.Periods[0].Net)

	assert.Equal(t, money.FromFloat(-3000), timeline.Periods[1].Net)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetMoneyFlowTimelineBillingCycles(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	userID := testUserID()
	acctID := uuid.New()
	cycleID := uuid.New()
	start := time.Date(2024, 5, 6, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 6, 5, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery("SELECT a.billing_day").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"billing_day"}).AddRow(intPtr(5)))
	expectBillingCyclesUpToDate(mock, userID, acctID, 5)
	mock.ExpectQuery("SELECT bc.id, bc.start_date, bc.end_date, bc.label").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "start_date", "end_date", "label", "net_activity", "txn_count"}).
			AddRow(cycleID, start, end, "Jun 2024", 500.0, 3))
	mock.ExpectQuery("FROM billing_cycles bc").
		WithArgs(acctID, userID, start.Format("2006-01-02"), end.Format("2006-01-02")).
		WillReturnRows(pgxmock.NewRows([]string{"id", "start_date", "end_date", "label", "income", "expense"}).
			AddRow(cycleID, start, end, "Jun 2024", 5000.0, 1500.0))

	req, _ := http.NewRequest(http.MethodGet,
		"/dashboard/money-flow/timeline?groupBy=billing_cycle&accountId="+acctID.String(), nil)
	w := httptest.NewRecorder()
	newMoneyFlowTimelineTestRouter(newTestServer(mock)).ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var timeline models.MoneyFlowTimeline
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &timeline))
	assert.Equal(t, "billing_cycle", timeline.GroupBy)
	require.Len(t, timeline.Periods, 1)
	assert.Equal(t, cycleID.String(), timeline.Periods[0].Key)
	assert.Equal(t, "Jun 2024", timeline.Periods[0].Label)
	assert.Equal(t, "2024-05-06", timeline.Periods[0].StartDate)
	assert.Equal(t, "2024-06-05", timeline.Periods[0].EndDate)
	assert.Equal(t, money.FromFloat(5000), timeline.Periods[0].Income)
	assert.Equal(t, money.FromFloat(1500), timeline.Periods[0].Expense)
	assert.Equal(t, money.FromFloat(3500), timeline.Periods[0].Net)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetMoneyFlowTimelineErrors(t *testing.T) {
	userID := testUserID()

	t.Run("invalid account id", func(t *testing.T) {
		w := httptest.NewRecorder()
		newMoneyFlowTimelineTestRouter(newTestServer(nil)).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "/dashboard/money-flow/timeline?accountId=nope", nil))
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("monthly query error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectQuery("date_trunc\\('month', t.date\\)").
			WithArgs(userID).
			WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		newMoneyFlowTimelineTestRouter(newTestServer(mock)).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "/dashboard/money-flow/timeline", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("billing cycle requires account id", func(t *testing.T) {
		w := httptest.NewRecorder()
		newMoneyFlowTimelineTestRouter(newTestServer(nil)).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "/dashboard/money-flow/timeline?groupBy=billing_cycle", nil))
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("billing cycle without billing day", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
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
		mock, err := pgxmock.NewPool()
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
