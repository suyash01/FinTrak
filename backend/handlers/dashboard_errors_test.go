package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fintrak/backend/internal/money"
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

	t.Run("transaction count error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE user_id").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions").
			WithArgs(userID).
			WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		newDashboardTestRouter(newTestServer(mock)).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/dashboard/summary", nil))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("currency scope error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE user_id").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
		mock.ExpectQuery("FROM accounts a\\s+LEFT JOIN transactions t").
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
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
		mock.ExpectQuery("FROM accounts a\\s+LEFT JOIN transactions t").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}))
		mock.ExpectQuery("t.type = 'debit' AND t.user_id").
			WithArgs(userID).
			WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		newDashboardTestRouter(newTestServer(mock)).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/dashboard/summary", nil))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("by category row error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		boom := errors.New("connection reset mid-fold")
		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE user_id").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))
		mock.ExpectQuery("FROM accounts a\\s+LEFT JOIN transactions t").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
				AddRow(uuid.New(), "Savings", "INR", money.FromFloat(100), money.FromFloat(40)))
		// A half-read fold would report the row that arrived and drop the one
		// that did not, so the error has to travel rather than shorten the list.
		mock.ExpectQuery("t.type = 'debit' AND t.user_id").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "currency", "total", "count"}).
				AddRow("c1", "Food", "#f00", "food", "INR", money.FromFloat(10), 1).
				AddRow("c2", "Rent", "#00f", "home", "INR", money.FromFloat(90), 1).
				RowError(1, boom))

		w := httptest.NewRecorder()
		newDashboardTestRouter(newTestServer(mock)).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/dashboard/summary", nil))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("monthly trend row error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE user_id").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))
		mock.ExpectQuery("FROM accounts a\\s+LEFT JOIN transactions t").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
				AddRow(uuid.New(), "Savings", "INR", money.FromFloat(100), money.FromFloat(40)))
		mock.ExpectQuery("t.type = 'debit' AND t.user_id").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "currency", "total", "count"}))
		mock.ExpectQuery("t.type = 'credit' AND t.user_id").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "currency", "total", "count"}))
		mock.ExpectQuery("TO_CHAR\\(t.date, 'YYYY-MM'\\)").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"month", "currency", "income", "expense"}).
				AddRow("2026-07", "INR", money.FromFloat(100), money.FromFloat(40)).
				RowError(0, errors.New("connection reset mid-fold")))

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
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
		mock.ExpectQuery("FROM accounts a\\s+LEFT JOIN transactions t").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}))
		mock.ExpectQuery("t.type = 'debit' AND t.user_id").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "currency", "total", "count"}))
		mock.ExpectQuery("t.type = 'credit' AND t.user_id").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "currency", "total", "count"}))
		mock.ExpectQuery("TO_CHAR\\(t.date, 'YYYY-MM'\\)").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"month", "currency", "income", "expense"}))
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
		// No cycle means no window, so every amount is an empty map and the
		// scope is empty rather than null: a client must not have to tell an
		// absent value from a nil one.
		var raw map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
		for _, field := range []string{"totalIncome", "totalExpense", "totalNet"} {
			amounts, isObject := raw[field].(map[string]any)
			if !isObject || len(amounts) != 0 {
				t.Errorf("%s = %v, want {}", field, raw[field])
			}
		}
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
		// A currency that is not three letters matches nothing, which would
		// render as a quiet month rather than an error.
		{name: "currency", query: "currency=US", errorText: "invalid currency"},
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
