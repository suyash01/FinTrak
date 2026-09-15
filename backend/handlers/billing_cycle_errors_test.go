package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newBillingCycleTestRouter(srv *Server) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.GET("/accounts/:id/billing-cycles", srv.GetBillingCycles)
	return r
}

func TestGetBillingCyclesErrors(t *testing.T) {
	t.Run("invalid id", func(t *testing.T) {
		r := newBillingCycleTestRouter(newTestServer(nil))

		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/accounts/not-a-uuid/billing-cycles", nil))

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("account lookup error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()
		r := newBillingCycleTestRouter(newTestServer(mock))

		acctID := uuid.New()
		mock.ExpectQuery("SELECT a.billing_day").
			WithArgs(acctID, testUserID()).
			WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/accounts/"+acctID.String()+"/billing-cycles", nil))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("ensure cycles error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()
		r := newBillingCycleTestRouter(newTestServer(mock))

		acctID := uuid.New()
		mock.ExpectQuery("SELECT a.billing_day").
			WithArgs(acctID, testUserID()).
			WillReturnRows(pgxmock.NewRows([]string{"billing_day"}).AddRow(intPtr(5)))
		mock.ExpectQuery("SELECT end_date FROM billing_cycles").
			WithArgs(acctID, testUserID()).
			WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/accounts/"+acctID.String()+"/billing-cycles", nil))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestEnsureBillingCyclesErrors(t *testing.T) {
	userID := testUserID()
	acctID := uuid.New()

	// Drop-misaligned query is the first call on every ensure path.
	expectCleanAlignment := func(mock pgxmock.PgxPoolIface) {
		mock.ExpectQuery("SELECT end_date FROM billing_cycles").
			WithArgs(acctID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"end_date"}))
	}

	t.Run("drop error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectQuery("SELECT end_date FROM billing_cycles").
			WithArgs(acctID, userID).
			WillReturnError(assert.AnError)

		assert.Error(t, ensureBillingCycles(context.Background(), mock, userID, acctID, 1))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("earliest query error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		expectCleanAlignment(mock)
		mock.ExpectQuery("MIN\\(date\\)").
			WithArgs(acctID, userID).
			WillReturnError(assert.AnError)

		assert.Error(t, ensureBillingCycles(context.Background(), mock, userID, acctID, 1))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("covered months query error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		expectCleanAlignment(mock)
		mock.ExpectQuery("MIN\\(date\\)").
			WithArgs(acctID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"min"}).AddRow(dateOnly(time.Now())))
		mock.ExpectQuery("SELECT end_date FROM billing_cycles").
			WithArgs(acctID, userID).
			WillReturnError(assert.AnError)

		assert.Error(t, ensureBillingCycles(context.Background(), mock, userID, acctID, 1))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("covered months scan error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		expectCleanAlignment(mock)
		mock.ExpectQuery("MIN\\(date\\)").
			WithArgs(acctID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"min"}).AddRow(dateOnly(time.Now())))
		mock.ExpectQuery("SELECT end_date FROM billing_cycles").
			WithArgs(acctID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"end_date"}).AddRow("not-a-time"))

		assert.Error(t, ensureBillingCycles(context.Background(), mock, userID, acctID, 1))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("unassigned probe error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		expectCleanAlignment(mock)
		mock.ExpectQuery("MIN\\(date\\)").
			WithArgs(acctID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"min"}).AddRow(dateOnly(time.Now())))
		mock.ExpectQuery("SELECT end_date FROM billing_cycles").
			WithArgs(acctID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"end_date"}))
		mock.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM transactions WHERE account_id").
			WithArgs(acctID, userID).
			WillReturnError(assert.AnError)

		assert.Error(t, ensureBillingCycles(context.Background(), mock, userID, acctID, 1))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("insert error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		today := dateOnly(time.Now())
		earliest := time.Date(today.Year(), today.Month(), 10, 0, 0, 0, 0, time.UTC)

		expectCleanAlignment(mock)
		mock.ExpectQuery("MIN\\(date\\)").
			WithArgs(acctID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"min"}).AddRow(earliest))
		mock.ExpectQuery("SELECT end_date FROM billing_cycles").
			WithArgs(acctID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"end_date"}))
		mock.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM transactions WHERE account_id").
			WithArgs(acctID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
		mock.ExpectExec("INSERT INTO billing_cycles").
			WithArgs(acctID, userID, pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnError(assert.AnError)

		assert.Error(t, ensureBillingCycles(context.Background(), mock, userID, acctID, 1))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("backfill error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		today := dateOnly(time.Now())
		earliest := time.Date(today.Year(), today.Month(), 10, 0, 0, 0, 0, time.UTC)

		expectCleanAlignment(mock)
		mock.ExpectQuery("MIN\\(date\\)").
			WithArgs(acctID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"min"}).AddRow(earliest))
		mock.ExpectQuery("SELECT end_date FROM billing_cycles").
			WithArgs(acctID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"end_date"}))
		mock.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM transactions WHERE account_id").
			WithArgs(acctID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
		for _, ms := range billingCycleMonths(earliest, today, 1) {
			start, end := cycleDates(ms, 1)
			mock.ExpectExec("INSERT INTO billing_cycles").
				WithArgs(acctID, userID, start, end, end.Format("Jan 2006")).
				WillReturnResult(pgxmock.NewResult("INSERT", 1))
		}
		mock.ExpectExec("UPDATE transactions t SET billing_cycle_id").
			WithArgs(acctID, userID).
			WillReturnError(assert.AnError)

		assert.Error(t, ensureBillingCycles(context.Background(), mock, userID, acctID, 1))
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestDropMisalignedCyclesErrors(t *testing.T) {
	userID := testUserID()
	acctID := uuid.New()

	t.Run("query error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectQuery("SELECT end_date FROM billing_cycles").
			WithArgs(acctID, userID).
			WillReturnError(assert.AnError)

		assert.Error(t, dropMisalignedCycles(context.Background(), mock, userID, acctID, 5))
	})

	t.Run("scan error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectQuery("SELECT end_date FROM billing_cycles").
			WithArgs(acctID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"end_date"}).AddRow("not-a-time"))

		assert.Error(t, dropMisalignedCycles(context.Background(), mock, userID, acctID, 5))
	})

	t.Run("rows error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		rows := pgxmock.NewRows([]string{"end_date"}).
			AddRow(time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC)).
			RowError(0, assert.AnError)
		mock.ExpectQuery("SELECT end_date FROM billing_cycles").
			WithArgs(acctID, userID).
			WillReturnRows(rows)

		assert.Error(t, dropMisalignedCycles(context.Background(), mock, userID, acctID, 5))
	})

	t.Run("detach error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		// End date on day 1 is misaligned with billing day 5.
		mock.ExpectQuery("SELECT end_date FROM billing_cycles").
			WithArgs(acctID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"end_date"}).
				AddRow(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)))
		mock.ExpectExec("UPDATE transactions SET billing_cycle_id = NULL").
			WithArgs(userID, acctID).
			WillReturnError(assert.AnError)

		assert.Error(t, dropMisalignedCycles(context.Background(), mock, userID, acctID, 5))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("delete error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectQuery("SELECT end_date FROM billing_cycles").
			WithArgs(acctID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"end_date"}).
				AddRow(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)))
		mock.ExpectExec("UPDATE transactions SET billing_cycle_id = NULL").
			WithArgs(userID, acctID).
			WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		mock.ExpectExec("DELETE FROM billing_cycles").
			WithArgs(acctID, userID).
			WillReturnError(assert.AnError)

		assert.Error(t, dropMisalignedCycles(context.Background(), mock, userID, acctID, 5))
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestListBillingCyclesErrors(t *testing.T) {
	userID := testUserID()
	acctID := uuid.New()

	t.Run("query error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectQuery("SELECT bc.id, bc.start_date").
			WithArgs(acctID, userID).
			WillReturnError(assert.AnError)

		_, err = listBillingCycles(context.Background(), mock, userID, acctID)
		assert.Error(t, err)
	})

	t.Run("scan error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectQuery("SELECT bc.id, bc.start_date").
			WithArgs(acctID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(uuid.New()))

		_, err = listBillingCycles(context.Background(), mock, userID, acctID)
		assert.Error(t, err)
	})
}
