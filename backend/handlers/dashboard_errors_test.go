package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// expectBillingCyclesUpToDate sets up the ensureBillingCycles queries for an
// account whose cycles are already present (nothing to generate or back-fill).
// The regeneration runs in its own transaction, so the block is wrapped in the
// Begin/Commit the handler's db.WithTx performs.
func expectBillingCyclesUpToDate(mock pgxmock.PgxPoolIface, userID, acctID uuid.UUID, billingDay int) {
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT end_date FROM billing_cycles").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"end_date"}))
	earliest := time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery("MIN\\(date\\)").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"min"}).AddRow(earliest))
	covered := pgxmock.NewRows([]string{"end_date"})
	for _, ms := range billingCycleMonths(earliest, dateOnly(time.Now()), billingDay) {
		_, end := cycleDates(ms, billingDay)
		covered.AddRow(end)
	}
	mock.ExpectQuery("SELECT end_date FROM billing_cycles").
		WithArgs(acctID, userID).
		WillReturnRows(covered)
	mock.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM transactions WHERE account_id").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectCommit()
}

func TestGetDashboardSummaryErrors(t *testing.T) {
	userID := testUserID()

	t.Run("begin error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}).
			WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		newDashboardTestRouter(newTestServer(mock)).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/dashboard/summary", nil))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("total accounts error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE user_id").
			WithArgs(userID).
			WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		newDashboardTestRouter(newTestServer(mock)).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/dashboard/summary", nil))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("totals error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE user_id").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
		mock.ExpectQuery("SELECT COUNT\\(\\*\\),").
			WithArgs(userID).
			WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		newDashboardTestRouter(newTestServer(mock)).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/dashboard/summary", nil))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("by category query error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE user_id").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
		mock.ExpectQuery("SELECT COUNT\\(\\*\\),").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"count", "income", "expense"}).AddRow(0, 0.0, 0.0))
		mock.ExpectQuery("t.type = 'debit' AND t.user_id").
			WithArgs(userID).
			WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		newDashboardTestRouter(newTestServer(mock)).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/dashboard/summary", nil))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("commit error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE user_id").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
		mock.ExpectQuery("SELECT COUNT\\(\\*\\),").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"count", "income", "expense"}).AddRow(0, 0.0, 0.0))
		mock.ExpectQuery("t.type = 'debit' AND t.user_id").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "total", "count"}))
		mock.ExpectQuery("t.type = 'credit' AND t.user_id").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "total", "count"}))
		mock.ExpectQuery("SELECT TO_CHAR\\(date, 'YYYY-MM'\\) as month").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"month", "income", "expense"}))
		mock.ExpectQuery("SELECT t.id, t.account_id, t.date, t.description, t.amount, t.type").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{
				"id", "account_id", "date", "description", "amount", "type",
				"category_id", "tags", "notes", "payee_id", "payee", "created_at",
				"account_name", "category_name", "category_icon", "category_color",
			}))
		mock.ExpectCommit().WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		newDashboardTestRouter(newTestServer(mock)).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/dashboard/summary", nil))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestGetDashboardSummaryBillingCycleErrors(t *testing.T) {
	userID := testUserID()

	t.Run("invalid account id", func(t *testing.T) {
		srv := newTestServer(nil)

		w := httptest.NewRecorder()
		newDashboardTestRouter(srv).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "/dashboard/summary?groupBy=billing_cycle&accountId=not-a-uuid", nil))

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("account lookup error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		acctID := uuid.New()
		mock.ExpectQuery("SELECT a.billing_day").
			WithArgs(acctID, userID).
			WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		newDashboardTestRouter(newTestServer(mock)).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "/dashboard/summary?groupBy=billing_cycle&accountId="+acctID.String(), nil))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("ensure cycles error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		acctID := uuid.New()
		mock.ExpectQuery("SELECT a.billing_day").
			WithArgs(acctID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"billing_day"}).AddRow(intPtr(5)))
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT end_date FROM billing_cycles").
			WithArgs(acctID, userID).
			WillReturnError(assert.AnError)
		mock.ExpectRollback()

		w := httptest.NewRecorder()
		newDashboardTestRouter(newTestServer(mock)).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "/dashboard/summary?groupBy=billing_cycle&accountId="+acctID.String(), nil))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("begin error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		acctID := uuid.New()
		mock.ExpectQuery("SELECT a.billing_day").
			WithArgs(acctID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"billing_day"}).AddRow(intPtr(5)))
		expectBillingCyclesUpToDate(mock, userID, acctID, 5)
		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}).
			WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		newDashboardTestRouter(newTestServer(mock)).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "/dashboard/summary?groupBy=billing_cycle&accountId="+acctID.String(), nil))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("list cycles error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		acctID := uuid.New()
		mock.ExpectQuery("SELECT a.billing_day").
			WithArgs(acctID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"billing_day"}).AddRow(intPtr(5)))
		expectBillingCyclesUpToDate(mock, userID, acctID, 5)
		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		mock.ExpectQuery("SELECT bc.id, bc.start_date").
			WithArgs(acctID, userID).
			WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		newDashboardTestRouter(newTestServer(mock)).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "/dashboard/summary?groupBy=billing_cycle&accountId="+acctID.String(), nil))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no cycles returns empty summary", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		acctID := uuid.New()
		mock.ExpectQuery("SELECT a.billing_day").
			WithArgs(acctID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"billing_day"}).AddRow(intPtr(5)))
		expectBillingCyclesUpToDate(mock, userID, acctID, 5)
		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		mock.ExpectQuery("SELECT bc.id, bc.start_date").
			WithArgs(acctID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"id", "start_date", "end_date", "label", "net_activity", "txn_count"}))
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE user_id").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))

		w := httptest.NewRecorder()
		newDashboardTestRouter(newTestServer(mock)).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "/dashboard/summary?groupBy=billing_cycle&accountId="+acctID.String(), nil))

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `"byCategory":[]`)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// The summary filters are compared against typed columns, so a malformed value
// must be a 400 rather than a Postgres cast/parse error surfacing as a 500.
func TestGetDashboardSummaryRejectsMalformedFilters(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		errorText string
	}{
		{name: "accountId", query: "accountId=not-a-uuid", errorText: "invalid accountId"},
		{name: "dateFrom", query: "dateFrom=2024-1-5", errorText: "dateFrom must be YYYY-MM-DD"},
		{name: "dateTo", query: "dateTo=not-a-date", errorText: "dateTo must be YYYY-MM-DD"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock, err := pgxmock.NewPool()
			require.NoError(t, err)
			defer mock.Close()

			w := httptest.NewRecorder()
			newDashboardTestRouter(newTestServer(mock)).ServeHTTP(w,
				httptest.NewRequest(http.MethodGet, "/dashboard/summary?"+tt.query, nil))

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), tt.errorText)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
