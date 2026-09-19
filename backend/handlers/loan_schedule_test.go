package handlers

import (
	"bytes"
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

// jsonRequest builds a JSON request for a handler test.
func jsonRequest(t *testing.T, method, path string, body any) *http.Request {
	t.Helper()
	buf, err := json.Marshal(body)
	require.NoError(t, err)
	req, _ := http.NewRequest(method, path, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func newLoanScheduleTestRouter(srv *Server) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.GET("/accounts/:id/loan-schedule", srv.GetLoanSchedule)
	r.PUT("/accounts/:id/loan-schedule", srv.UpsertLoanSchedule)
	r.DELETE("/accounts/:id/loan-schedule", srv.DeleteLoanSchedule)
	return r
}

func TestLoanAmortizationZeroRateSplitsEvenly(t *testing.T) {
	start := time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC)
	emi, entries := loanAmortization(money.FromFloat(1200), 0, 12, start)

	assert.Equal(t, money.FromFloat(100), emi)
	require.Len(t, entries, 12)
	for i, e := range entries {
		assert.Equal(t, i+1, e.Number)
		assert.Equal(t, money.FromFloat(100), e.Amount)
		assert.Equal(t, money.FromFloat(100), e.Principal)
		assert.Zero(t, e.Interest)
		assert.Equal(t, money.FromFloat(1200)-money.FromFloat(100)*money.Amount(i+1), e.Balance)
	}
	// The anchor day is clamped to each month's length rather than drifting.
	assert.Equal(t, "2024-01-31", entries[0].DueDate.Format("2006-01-02"))
	assert.Equal(t, "2024-02-29", entries[1].DueDate.Format("2006-01-02"))
	assert.Equal(t, "2024-12-31", entries[11].DueDate.Format("2006-01-02"))
}

func TestLoanAmortizationSplitsPrincipalAndInterest(t *testing.T) {
	start := time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC)
	// 1000.00 at 12% p.a. over 12 months: 88.85 per month.
	emi, entries := loanAmortization(money.FromFloat(1000), 1200, 12, start)

	assert.Equal(t, money.FromFloat(88.85), emi)
	require.Len(t, entries, 12)

	first := entries[0]
	assert.Equal(t, money.FromFloat(88.85), first.Amount)
	assert.Equal(t, money.FromFloat(10), first.Interest)
	assert.Equal(t, money.FromFloat(78.85), first.Principal)
	assert.Equal(t, money.FromFloat(921.15), first.Balance)

	// The table repays the principal exactly: every installment's split sums to
	// its amount and the final balance is zero.
	var principalSum, interestSum, payable money.Amount
	for _, e := range entries {
		assert.Equal(t, e.Amount, e.Principal+e.Interest)
		principalSum += e.Principal
		interestSum += e.Interest
		payable += e.Amount
	}
	assert.Equal(t, money.FromFloat(1000), principalSum)
	assert.Zero(t, entries[len(entries)-1].Balance)
	assert.Equal(t, principalSum+interestSum, payable)
	// Interest dominates the early installments and shrinks over time.
	assert.Greater(t, first.Interest, entries[len(entries)-1].Interest)
	assert.Greater(t, entries[len(entries)-1].Principal, first.Principal)
}

func TestGetLoanScheduleWithoutSchedule(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	srv := newTestServer(mock)
	r := newLoanScheduleTestRouter(srv)

	userID := testUserID()
	accountID := uuid.New()

	mock.ExpectQuery("SELECT name, account_type_id FROM accounts").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"name", "account_type_id"}).AddRow("Car Loan", "loan"))
	mock.ExpectQuery("FROM loan_schedules").
		WithArgs(accountID, userID).
		WillReturnError(pgx.ErrNoRows)

	req, _ := http.NewRequest(http.MethodGet, "/accounts/"+accountID.String()+"/loan-schedule", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var detail models.LoanScheduleDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
	assert.Nil(t, detail.Schedule)
	assert.Empty(t, detail.Entries)
	assert.Equal(t, "Car Loan", detail.LoanAccountName)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetLoanScheduleWithProgress(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	srv := newTestServer(mock)
	r := newLoanScheduleTestRouter(srv)

	userID := testUserID()
	accountID := uuid.New()
	scheduleID := uuid.New()
	paidTxn, refundTxn := uuid.New(), uuid.New()
	created := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery("SELECT name, account_type_id FROM accounts").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"name", "account_type_id"}).AddRow("Car Loan", "loan"))
	mock.ExpectQuery("FROM loan_schedules").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "loan_account_id", "principal", "annual_rate_bps", "tenure_months", "start_date", "created_at", "updated_at"}).
			AddRow(scheduleID, accountID, money.FromFloat(1000), 1200, 12, time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC), created, created))
	mock.ExpectQuery("FROM loan_attachments").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "amount", "type"}).
			AddRow(paidTxn, money.FromFloat(88.85), "debit").
			AddRow(refundTxn, money.FromFloat(10), "credit"))

	req, _ := http.NewRequest(http.MethodGet, "/accounts/"+accountID.String()+"/loan-schedule", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var detail models.LoanScheduleDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
	require.NotNil(t, detail.Schedule)
	assert.Equal(t, money.FromFloat(88.85), detail.EMI)
	assert.Len(t, detail.Entries, 12)

	// The single debit covers the first installment; the refund reduces what was
	// paid without covering another one.
	assert.Equal(t, 1, detail.PaidInstallments)
	assert.True(t, detail.Entries[0].Paid)
	require.NotNil(t, detail.Entries[0].TransactionID)
	assert.Equal(t, paidTxn, *detail.Entries[0].TransactionID)
	assert.False(t, detail.Entries[1].Paid)
	assert.Equal(t, money.FromFloat(78.85), detail.PaidAmount) // 88.85 - 10
	assert.Equal(t, money.FromFloat(78.85), detail.PrincipalPaid)
	assert.Equal(t, money.FromFloat(10), detail.InterestPaid)
	assert.Equal(t, money.FromFloat(921.15), detail.OutstandingPrincipal)
	assert.False(t, detail.Completed)
	require.NotNil(t, detail.NextDueDate)
	assert.Equal(t, "2024-05-01", detail.NextDueDate.Format("2006-01-02"))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetLoanScheduleAccountErrors(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()
		r := newLoanScheduleTestRouter(newTestServer(mock))
		accountID := uuid.New()

		mock.ExpectQuery("SELECT name, account_type_id FROM accounts").
			WithArgs(accountID, testUserID()).
			WillReturnError(pgx.ErrNoRows)

		req, _ := http.NewRequest(http.MethodGet, "/accounts/"+accountID.String()+"/loan-schedule", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not a loan account", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()
		r := newLoanScheduleTestRouter(newTestServer(mock))
		accountID := uuid.New()

		mock.ExpectQuery("SELECT name, account_type_id FROM accounts").
			WithArgs(accountID, testUserID()).
			WillReturnRows(pgxmock.NewRows([]string{"name", "account_type_id"}).AddRow("Checking", "bank"))

		req, _ := http.NewRequest(http.MethodGet, "/accounts/"+accountID.String()+"/loan-schedule", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "not a loan account")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("invalid id", func(t *testing.T) {
		srv, _ := newMockServer(t)
		r := newLoanScheduleTestRouter(srv)

		req, _ := http.NewRequest(http.MethodGet, "/accounts/nope/loan-schedule", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestUpsertLoanScheduleValidatesInput(t *testing.T) {
	accountID := uuid.NewString()
	base := func(overrides map[string]any) map[string]any {
		body := map[string]any{
			"principal":     1000,
			"annualRateBps": 1200,
			"tenureMonths":  12,
			"startDate":     "2024-04-01",
		}
		for k, v := range overrides {
			body[k] = v
		}
		return body
	}

	cases := []struct {
		name string
		body map[string]any
		want string
	}{
		{"zero principal", base(map[string]any{"principal": 0}), "principal is required"},
		{"negative principal", base(map[string]any{"principal": -5}), "principal must be positive"},
		{"negative rate", base(map[string]any{"annualRateBps": -1}), "annualRateBps must not be negative"},
		{"zero tenure", base(map[string]any{"tenureMonths": 0}), "tenureMonths is required"},
		{"long tenure", base(map[string]any{"tenureMonths": 601}), "tenureMonths must be between 1 and 600"},
		{"bad date", base(map[string]any{"startDate": "01-04-2024"}), "invalid date"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newMockServer(t)
			r := newLoanScheduleTestRouter(srv)

			req := jsonRequest(t, http.MethodPut, "/accounts/"+accountID+"/loan-schedule", tc.body)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), tc.want)
		})
	}
}

func TestUpsertLoanScheduleStoresAndReturnsTable(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	srv := newTestServer(mock)
	r := newLoanScheduleTestRouter(srv)

	userID := testUserID()
	accountID := uuid.New()
	created := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	start := time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery("SELECT name, account_type_id FROM accounts").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"name", "account_type_id"}).AddRow("Car Loan", "loan"))
	mock.ExpectExec("INSERT INTO loan_schedules").
		WithArgs(accountID, userID, money.FromFloat(1000), 1200, 12, start).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectQuery("FROM loan_schedules").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "loan_account_id", "principal", "annual_rate_bps", "tenure_months", "start_date", "created_at", "updated_at"}).
			AddRow(uuid.New(), accountID, money.FromFloat(1000), 1200, 12, start, created, created))
	mock.ExpectQuery("FROM loan_attachments").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "amount", "type"}))

	body := map[string]any{
		"principal":     1000,
		"annualRateBps": 1200,
		"tenureMonths":  12,
		"startDate":     "2024-04-01",
	}
	req := jsonRequest(t, http.MethodPut, "/accounts/"+accountID.String()+"/loan-schedule", body)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var detail models.LoanScheduleDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
	require.NotNil(t, detail.Schedule)
	assert.Equal(t, money.FromFloat(88.85), detail.EMI)
	assert.Len(t, detail.Entries, 12)
	assert.Equal(t, "Car Loan", detail.LoanAccountName)
	assert.Equal(t, 0, detail.PaidInstallments)
	assert.Equal(t, money.FromFloat(1000), detail.OutstandingPrincipal)
	assert.Greater(t, detail.TotalInterest, money.Amount(0))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteLoanSchedule(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	srv := newTestServer(mock)
	r := newLoanScheduleTestRouter(srv)

	userID := testUserID()
	accountID := uuid.New()

	mock.ExpectQuery("SELECT name, account_type_id FROM accounts").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"name", "account_type_id"}).AddRow("Car Loan", "loan"))
	mock.ExpectExec("DELETE FROM loan_schedules").
		WithArgs(accountID, userID).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	req, _ := http.NewRequest(http.MethodDelete, "/accounts/"+accountID.String()+"/loan-schedule", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"deleted": 1}`, w.Body.String())
	assert.NoError(t, mock.ExpectationsWereMet())
}
