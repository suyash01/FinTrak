package handlers

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fintrak/backend/internal/money"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMergeMonthEndRows(t *testing.T) {
	day := func(m, d int) time.Time {
		return time.Date(2024, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	}

	t.Run("no rows returns transactions unchanged", func(t *testing.T) {
		txns := []models.Transaction{{Date: day(1, 10)}}
		assert.Equal(t, txns, mergeMonthEndRows(txns, nil, "ASC"))
	})

	t.Run("ascending interleaves rows after their month", func(t *testing.T) {
		txns := []models.Transaction{{Date: day(1, 10)}, {Date: day(1, 20)}, {Date: day(2, 5)}}
		rows := []models.Transaction{{Date: day(1, 31)}, {Date: day(2, 28)}}

		got := mergeMonthEndRows(txns, rows, "ASC")

		require.Len(t, got, 5)
		assert.Equal(t, day(1, 10), dateOnly(got[0].Date))
		assert.Equal(t, day(1, 20), dateOnly(got[1].Date))
		assert.Equal(t, day(1, 31), dateOnly(got[2].Date))
		assert.Equal(t, day(2, 5), dateOnly(got[3].Date))
		assert.Equal(t, day(2, 28), dateOnly(got[4].Date))
	})

	t.Run("descending interleaves rows before their month", func(t *testing.T) {
		txns := []models.Transaction{{Date: day(2, 5)}, {Date: day(1, 20)}, {Date: day(1, 10)}}
		rows := []models.Transaction{{Date: day(2, 28)}, {Date: day(1, 31)}}

		got := mergeMonthEndRows(txns, rows, "DESC")

		require.Len(t, got, 5)
		assert.Equal(t, day(2, 28), dateOnly(got[0].Date))
		assert.Equal(t, day(2, 5), dateOnly(got[1].Date))
		assert.Equal(t, day(1, 31), dateOnly(got[2].Date))
		assert.Equal(t, day(1, 20), dateOnly(got[3].Date))
		assert.Equal(t, day(1, 10), dateOnly(got[4].Date))
	})

	t.Run("rows with no page transaction are dropped", func(t *testing.T) {
		txns := []models.Transaction{{Date: day(3, 5)}}
		rows := []models.Transaction{{Date: day(1, 31)}}

		assert.Equal(t, txns, mergeMonthEndRows(txns, rows, "ASC"))
	})
}

func TestMergeSummaryRowsBranches(t *testing.T) {
	day := func(m, d int) time.Time {
		return time.Date(2024, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	}
	c1, c2 := uuid.New(), uuid.New()

	t.Run("non-date sort hides summary rows", func(t *testing.T) {
		txns := []models.Transaction{{Date: day(1, 10)}}
		rows := []models.Transaction{{Date: day(1, 31), BillingCycleID: &c1}}
		assert.Equal(t, txns, mergeSummaryRows(txns, rows, "amount", "DESC"))
	})

	t.Run("empty rows returns transactions", func(t *testing.T) {
		txns := []models.Transaction{{Date: day(1, 10)}}
		assert.Equal(t, txns, mergeSummaryRows(txns, nil, "date", "ASC"))
	})

	t.Run("rows off the page are dropped", func(t *testing.T) {
		txns := []models.Transaction{{Date: day(3, 5)}}
		rows := []models.Transaction{{Date: day(1, 31), BillingCycleID: &c1}}
		assert.Equal(t, txns, mergeSummaryRows(txns, rows, "date", "ASC"))
	})

	t.Run("interleaves grouped rows by cycle", func(t *testing.T) {
		txns := []models.Transaction{
			{Date: day(1, 10), BillingCycleID: &c1},
			{Date: day(2, 3), BillingCycleID: &c2},
		}
		rows := []models.Transaction{
			{Date: day(1, 31), BillingCycleID: &c1},
			{Date: day(2, 28), BillingCycleID: &c2},
		}

		got := mergeSummaryRows(txns, rows, "date", "ASC")

		require.Len(t, got, 4)
		assert.Equal(t, day(1, 10), dateOnly(got[0].Date))
		assert.Equal(t, day(1, 31), dateOnly(got[1].Date))
		assert.Equal(t, day(2, 3), dateOnly(got[2].Date))
		assert.Equal(t, day(2, 28), dateOnly(got[3].Date))
	})
}

func TestComputeMonthEndBalanceRows(t *testing.T) {
	day := func(m, d int) time.Time {
		return time.Date(2024, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	}

	newCtx := func(t *testing.T) *gin.Context {
		t.Helper()
		gin.SetMode(gin.TestMode)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		return c
	}

	t.Run("monthly net query error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()
		srv := newTestServer(mock)

		acctID := uuid.New()
		mock.ExpectQuery("date_trunc\\('month', t.date\\)").
			WithArgs(acctID, testUserID()).
			WillReturnError(assert.AnError)

		assert.Nil(t, srv.computeMonthEndBalanceRows(newCtx(t), testUserID(), acctID, "Acct", "", ""))
	})

	t.Run("scan error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()
		srv := newTestServer(mock)

		acctID := uuid.New()
		mock.ExpectQuery("date_trunc\\('month', t.date\\)").
			WithArgs(acctID, testUserID()).
			WillReturnRows(pgxmock.NewRows([]string{"month"}).AddRow(day(1, 1)))

		assert.Nil(t, srv.computeMonthEndBalanceRows(newCtx(t), testUserID(), acctID, "Acct", "", ""))
	})

	t.Run("no activity", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()
		srv := newTestServer(mock)

		acctID := uuid.New()
		mock.ExpectQuery("date_trunc\\('month', t.date\\)").
			WithArgs(acctID, testUserID()).
			WillReturnRows(pgxmock.NewRows([]string{"month", "net", "count"}))

		assert.Nil(t, srv.computeMonthEndBalanceRows(newCtx(t), testUserID(), acctID, "Acct", "", ""))
	})

	t.Run("completed and in-progress months", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()
		srv := newTestServer(mock)

		acctID := uuid.New()
		userID := testUserID()
		mock.ExpectQuery("date_trunc\\('month', t.date\\)").
			WithArgs(acctID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"month", "net", "count"}).
				AddRow(day(2, 1), money.FromFloat(100), 1).
				AddRow(day(3, 1), money.FromFloat(50), 1))
		mock.ExpectQuery("SELECT COALESCE\\(SUM\\(CASE WHEN t.type = 'credit'").
			WithArgs(acctID, userID, day(3, 15)).
			WillReturnRows(pgxmock.NewRows([]string{"total", "count"}).AddRow(money.FromFloat(410), 3))

		rows := srv.computeMonthEndBalanceRows(newCtx(t), userID, acctID, "Acct", "", "2024-03-15")

		require.Len(t, rows, 2)
		assert.Equal(t, day(2, 29), dateOnly(rows[0].Date))
		assert.Equal(t, money.FromFloat(100), rows[0].Amount)
		assert.True(t, rows[0].IsSummary)
		assert.Equal(t, day(3, 15), dateOnly(rows[1].Date))
		assert.Equal(t, money.FromFloat(410), rows[1].Amount)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("in-progress month with no activity is skipped", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()
		srv := newTestServer(mock)

		acctID := uuid.New()
		userID := testUserID()
		mock.ExpectQuery("date_trunc\\('month', t.date\\)").
			WithArgs(acctID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"month", "net", "count"}).
				AddRow(day(3, 1), money.FromFloat(0), 0))

		assert.Empty(t, srv.computeMonthEndBalanceRows(newCtx(t), userID, acctID, "Acct", "", "2024-03-15"))
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestBuildAccountSummaryRows(t *testing.T) {
	t.Run("account lookup error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()
		srv := newTestServer(mock)

		gin.SetMode(gin.TestMode)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		acctID := uuid.New()

		mock.ExpectQuery("SELECT a.name, a.billing_day").
			WithArgs(acctID, testUserID()).
			WillReturnError(assert.AnError)

		cycles, balances := srv.buildAccountSummaryRows(c, testUserID(), acctID, "", "")
		assert.Nil(t, cycles)
		assert.Nil(t, balances)
	})

	t.Run("ensure cycles error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()
		srv := newTestServer(mock)

		gin.SetMode(gin.TestMode)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		acctID := uuid.New()

		mock.ExpectQuery("SELECT a.name, a.billing_day").
			WithArgs(acctID, testUserID()).
			WillReturnRows(pgxmock.NewRows([]string{"name", "billing_day"}).AddRow("Amex", intPtr(5)))
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT end_date FROM billing_cycles").
			WithArgs(acctID, testUserID()).
			WillReturnError(assert.AnError)
		mock.ExpectRollback()

		cycles, balances := srv.buildAccountSummaryRows(c, testUserID(), acctID, "", "")
		assert.Nil(t, cycles)
		assert.Nil(t, balances)
	})

	t.Run("no billing day falls back to month-end balances", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()
		srv := newTestServer(mock)

		gin.SetMode(gin.TestMode)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		acctID := uuid.New()
		userID := testUserID()

		mock.ExpectQuery("SELECT a.name, a.billing_day").
			WithArgs(acctID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"name", "billing_day"}).AddRow("Bank", nil))
		mock.ExpectQuery("date_trunc\\('month', t.date\\)").
			WithArgs(acctID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"month", "net", "count"}))

		cycles, balances := srv.buildAccountSummaryRows(c, userID, acctID, "", "")
		assert.Nil(t, cycles)
		assert.Empty(t, balances)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
