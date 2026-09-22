package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fintrak/backend/internal/money"
	"github.com/fintrak/backend/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Settlement quotes (GET /accounts/:id/loan-payoff): the principal a loan still
// owes plus the interest accrued since its last EMI payment.

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// payoffDetail is a loan detail with the figures a quote needs: 1,000.00 owed at
// 12% p.a., optionally with a payment date already recorded.
func payoffDetail(lastPaid *time.Time, disbursal *time.Time, start time.Time) models.LoanScheduleDetail {
	return models.LoanScheduleDetail{
		Schedule: &models.LoanSchedule{
			Principal:     money.FromFloat(1200),
			AnnualRateBps: 1200,
			TenureMonths:  12,
			StartDate:     start,
			DisbursalDate: disbursal,
		},
		OutstandingPrincipal: money.FromFloat(1000),
		LastPaidDate:         lastPaid,
	}
}

func TestLoanPayoffAccruesFromTheLastPayment(t *testing.T) {
	paid := date(2024, 2, 10)
	// 1,000.00 at 12% p.a. is 10.00 a month; 2024-02-10 to 2024-03-15 is 34
	// days, so 34/30 of a month.
	quote := loanPayoffFor(payoffDetail(&paid, nil, date(2024, 1, 10)), date(2024, 3, 15))

	assert.Equal(t, date(2024, 2, 10), quote.FromDate)
	assert.Equal(t, 34, quote.Days)
	assert.Equal(t, money.FromFloat(1000), quote.OutstandingPrincipal)
	assert.Equal(t, money.FromFloat(11.33), quote.AccruedInterest)
	assert.Equal(t, money.FromFloat(1011.33), quote.Payoff)
	assert.Equal(t, quote.OutstandingPrincipal+quote.AccruedInterest, quote.Payoff)
}

// Paying an installment is what clears its interest, so a settlement dated
// before the last payment accrues nothing.
func TestLoanPayoffBeforeTheLastPaymentAccruesNothing(t *testing.T) {
	paid := date(2024, 2, 10)
	quote := loanPayoffFor(payoffDetail(&paid, nil, date(2024, 1, 10)), date(2024, 2, 1))

	assert.Equal(t, 0, quote.Days)
	assert.Zero(t, quote.AccruedInterest)
	assert.Equal(t, quote.OutstandingPrincipal, quote.Payoff)
}

// Nothing paid yet: the interest runs from when the money was released, which is
// the disbursal date when it is known and otherwise the month before the first
// installment.
func TestLoanPayoffFallsBackToTheDisbursalDate(t *testing.T) {
	disbursal := date(2024, 2, 20)
	quote := loanPayoffFor(payoffDetail(nil, &disbursal, date(2024, 4, 5)), date(2024, 4, 5))

	assert.Equal(t, date(2024, 2, 20), quote.FromDate)
	assert.Equal(t, 45, quote.Days)
	assert.Equal(t, money.FromFloat(15), quote.AccruedInterest) // 45/30 of a month on 1,000.00
	assert.Equal(t, money.FromFloat(1015), quote.Payoff)
}

func TestLoanPayoffWithoutADisbursalDateAssumesTheFirstPeriod(t *testing.T) {
	quote := loanPayoffFor(payoffDetail(nil, nil, date(2024, 4, 5)), date(2024, 4, 5))

	assert.Equal(t, date(2024, 3, 5), quote.FromDate)
	assert.Equal(t, 31, quote.Days)
	assert.Equal(t, money.FromFloat(10.33), quote.AccruedInterest)
	assert.Equal(t, money.FromFloat(1010.33), quote.Payoff)
}

func TestGetLoanPayoffQuotesTheTransferAmount(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	srv := newTestServer(mock)
	r := newLoanScheduleTestRouter(srv)

	userID := testUserID()
	accountID := uuid.New()
	paid := date(2024, 2, 10)

	mock.ExpectQuery("SELECT name, account_type_id FROM accounts").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"name", "account_type_id"}).AddRow("Car Loan", "loan"))
	expectLoanScheduleRows(mock, accountID, userID,
		[]any{uuid.New(), accountID, money.FromFloat(1200), money.FromFloat(0), 1200, 12,
			date(2024, 1, 10), nil, paid, paid})
	expectNoLoanTransfers(mock, accountID, userID)
	expectLoanDisbursementCredit(mock, accountID, userID)
	expectLoanPayments(mock, accountID, userID,
		[]any{uuid.New(), money.FromFloat(107), date(2024, 1, 10), "debit"},
		[]any{uuid.New(), money.FromFloat(107), paid, "debit"})

	req, _ := http.NewRequest(http.MethodGet,
		"/accounts/"+accountID.String()+"/loan-payoff?date=2024-03-15", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var quote models.LoanPayoff
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &quote))
	assert.Equal(t, "Car Loan", quote.LoanAccountName)
	assert.Equal(t, date(2024, 2, 10), quote.FromDate)
	assert.Equal(t, 34, quote.Days)
	// Two 107.00 installments paid off 190.95 of the 1,200.00 principal.
	assert.Equal(t, money.FromFloat(1009.05), quote.OutstandingPrincipal)
	assert.Equal(t, money.FromFloat(11.44), quote.AccruedInterest)
	assert.Equal(t, money.FromFloat(1020.49), quote.Payoff)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetLoanPayoffRejections(t *testing.T) {
	userID := testUserID()
	accountID := uuid.New()
	paid := date(2024, 2, 10)

	t.Run("no date", func(t *testing.T) {
		srv, _ := newMockServer(t)
		r := newLoanScheduleTestRouter(srv)

		req, _ := http.NewRequest(http.MethodGet, "/accounts/"+accountID.String()+"/loan-payoff", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "invalid date")
	})

	t.Run("invalid id", func(t *testing.T) {
		srv, _ := newMockServer(t)
		r := newLoanScheduleTestRouter(srv)

		req, _ := http.NewRequest(http.MethodGet, "/accounts/nope/loan-payoff?date=2024-03-15", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("loan has no schedule", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()
		r := newLoanScheduleTestRouter(newTestServer(mock))

		mock.ExpectQuery("SELECT name, account_type_id FROM accounts").
			WithArgs(accountID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"name", "account_type_id"}).AddRow("Car Loan", "loan"))
		mock.ExpectQuery("FROM loan_schedules").WithArgs(accountID, userID).WillReturnError(pgx.ErrNoRows)

		req, _ := http.NewRequest(http.MethodGet, "/accounts/"+accountID.String()+"/loan-payoff?date=2024-03-15", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "no amortization schedule")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("loan is already settled", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()
		r := newLoanScheduleTestRouter(newTestServer(mock))

		mock.ExpectQuery("SELECT name, account_type_id FROM accounts").
			WithArgs(accountID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"name", "account_type_id"}).AddRow("Car Loan", "loan"))
		expectLoanScheduleRows(mock, accountID, userID,
			[]any{uuid.New(), accountID, money.FromFloat(1200), money.FromFloat(0), 1200, 12,
				date(2024, 1, 10), nil, paid, paid})
		mock.ExpectQuery("FROM loan_transfers").
			WithArgs(accountID, userID).
			WillReturnRows(pgxmock.NewRows(loanTransferCols).
				AddRow(transferRowJSON(uuid.New(), accountID, uuid.New(), money.FromFloat(1000), money.FromFloat(1000), paid, models.LoanTransferRecast)...))
		expectLoanDisbursementCredit(mock, accountID, userID)
		expectLoanPayments(mock, accountID, userID)

		req, _ := http.NewRequest(http.MethodGet, "/accounts/"+accountID.String()+"/loan-payoff?date=2024-03-15", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "already settled")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
