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
	r.POST("/accounts/:id/loan-transfer", srv.TransferLoanBalance)
	r.DELETE("/accounts/:id/loan-transfer/:transferId", srv.DeleteLoanTransfer)
	r.GET("/accounts/:id/loan-payoff", srv.GetLoanPayoff)
	r.PUT("/accounts/:id/loan-disbursement", srv.LinkLoanDisbursement)
	r.DELETE("/accounts/:id/loan-disbursement", srv.UnlinkLoanDisbursement)
	return r
}

// loanScheduleCols and loanTransferCols are the columns the detail loader
// reads, in order. The mock matches rows positionally, so a query that gains a
// column has to be reflected here.
var loanScheduleCols = []string{"id", "loan_account_id", "principal", "processing_fee", "annual_rate_bps",
	"tenure_months", "start_date", "disbursal_date", "created_at", "updated_at"}

var loanTransferCols = []string{"id", "from_loan_account_id", "from_name", "to_loan_account_id", "to_name",
	"amount", "principal", "transfer_date", "mode", "created_at"}

// expectNoLoanTransfers matches the transfers read of a loan that took part in
// none, which every detail load performs.
func expectNoLoanTransfers(mock pgxmock.PgxPoolIface, accountID, userID any) {
	mock.ExpectQuery("FROM loan_transfers").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows(loanTransferCols))
}

// expectLoanScheduleRows matches the schedule read for a set of terms.
func expectLoanScheduleRows(mock pgxmock.PgxPoolIface, accountID any, userID any, rows ...[]any) {
	mock.ExpectQuery("FROM loan_schedules").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows(loanScheduleCols).AddRows(rows...))
}

func TestLoanAmortizationZeroRateSplitsEvenly(t *testing.T) {
	start := time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC)
	emi, entries := loanAmortization(loanTerms{
		principal:    money.FromFloat(1200),
		tenureMonths: 12,
		firstDueDate: start,
	}, nil)

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
	// 1000.00 at 12% p.a. over 12 months is an exact annuity of 88.85, which a
	// lender quotes as the whole rupee 89.00.
	emi, entries := loanAmortization(loanTerms{
		principal:     money.FromFloat(1000),
		annualRateBps: 1200,
		tenureMonths:  12,
		firstDueDate:  start,
	}, nil)

	assert.Equal(t, money.FromFloat(89), emi)
	require.Len(t, entries, 12)

	first := entries[0]
	assert.Equal(t, money.FromFloat(89), first.Amount)
	assert.Equal(t, money.FromFloat(10), first.Interest)
	assert.Equal(t, money.FromFloat(79), first.Principal)
	assert.Equal(t, money.FromFloat(921), first.Balance)

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

// The processing fee is recorded for reference and never amortized: the table
// repays the whole principal, so it is identical to a loan without a fee.
func TestLoanAmortizationIgnoresProcessingFee(t *testing.T) {
	start := time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC)
	withoutFee, plain := loanAmortization(loanTerms{
		principal:     money.FromFloat(1000),
		annualRateBps: 1200,
		tenureMonths:  12,
		firstDueDate:  start,
	}, nil)

	withFee, entries := loanAmortization(loanTerms{
		principal:     money.FromFloat(1000),
		processingFee: money.FromFloat(100),
		annualRateBps: 1200,
		tenureMonths:  12,
		firstDueDate:  start,
	}, nil)

	assert.Equal(t, withoutFee, withFee)
	assert.Equal(t, plain, entries)
	assert.Equal(t, money.FromFloat(89), withFee)
	assert.Equal(t, money.FromFloat(10), entries[0].Interest)

	var principalSum money.Amount
	for _, e := range entries {
		principalSum += e.Principal
	}
	assert.Equal(t, money.FromFloat(1000), principalSum)
	assert.Zero(t, entries[len(entries)-1].Balance)
}

// The EMI is rounded up to the whole rupee, so it eventually retires the
// balance before the last installment. The surplus must simply not be charged:
// clamping the principal part at what is left keeps every figure the API
// returns non-negative, while the table still repays exactly the principal.
//
// Before the clamp these tables ended negative — 50,000.00 at 850 bps over 360
// months quoted a final installment of -511.96 (principal -50,836), and a
// negative installment matched as "paid" subtracted from the loan.
func TestLoanAmortizationNeverEmitsNegativeInstallments(t *testing.T) {
	start := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name      string
		principal money.Amount
		bps       int
		months    int
	}{
		{"fifty thousand over 360 months", money.FromFloat(50000), 850, 360},
		{"eighty thousand over 360 months", money.FromFloat(80000), 850, 360},
		{"thirty thousand over 240 months", money.FromFloat(30000), 850, 240},
	} {
		t.Run(tc.name, func(t *testing.T) {
			emi, entries := loanAmortization(loanTerms{
				principal:     tc.principal,
				annualRateBps: tc.bps,
				tenureMonths:  tc.months,
				firstDueDate:  start,
			}, nil)

			require.Len(t, entries, tc.months)
			require.Positive(t, emi)

			var principalSum money.Amount
			for _, e := range entries {
				zero := int64(0)
				assert.GreaterOrEqual(t, int64(e.Principal), zero, "installment %d principal", e.Number)
				assert.GreaterOrEqual(t, int64(e.Interest), zero, "installment %d interest", e.Number)
				assert.GreaterOrEqual(t, int64(e.Amount), zero, "installment %d amount", e.Number)
				assert.GreaterOrEqual(t, int64(e.Balance), zero, "installment %d balance", e.Number)
				assert.Equal(t, e.Amount, e.Principal+e.Interest, "installment %d split", e.Number)
				principalSum += e.Principal
			}
			// The two invariants the table has always held: the principal is
			// repaid exactly, and nothing is owed after the last installment.
			assert.Equal(t, tc.principal, principalSum)
			assert.Zero(t, entries[len(entries)-1].Balance)
		})
	}
}

// A loan disbursed on the 20th with its EMIs fixed to the 5th has a broken first
// period. That period is charged 45/30 of a month's interest, and the EMI is
// solved so the loan still clears in twelve level installments — so the first
// installment leans to interest instead of the difference ballooning the last.
func TestLoanAmortizationFirstPeriodStubChargesDayCountInterest(t *testing.T) {
	start := time.Date(2024, 4, 5, 0, 0, 0, 0, time.UTC)
	disbursal := time.Date(2024, 2, 20, 0, 0, 0, 0, time.UTC)

	emi, entries := loanAmortization(loanTerms{
		principal:     money.FromFloat(1000),
		annualRateBps: 1200,
		tenureMonths:  12,
		firstDueDate:  start,
		disbursalDate: &disbursal,
	}, nil)

	require.Len(t, entries, 12)
	// 45 days (2024-02-20 -> 2024-04-05) prorated over a 30-day month: 15.00.
	assert.Equal(t, money.FromFloat(15), entries[0].Interest)
	assert.Equal(t, emi-money.FromFloat(15), entries[0].Principal)
	assert.Equal(t, emi, entries[0].Amount)

	// Every later installment is a plain month: one twelfth of the rate, on the
	// smaller balance the stub left.
	assert.Equal(t, money.FromFloat(9.25), entries[1].Interest)
	assert.Equal(t, emi-money.FromFloat(9.25), entries[1].Principal)
	assert.Greater(t, entries[0].Interest, entries[1].Interest)
	assert.Less(t, entries[0].Principal, entries[1].Principal)

	// The stub shifts the split, not the repayment: the loan still clears.
	var principalSum money.Amount
	for _, e := range entries {
		assert.Equal(t, e.Amount, e.Principal+e.Interest)
		principalSum += e.Principal
	}
	assert.Equal(t, money.FromFloat(1000), principalSum)
	assert.Zero(t, entries[len(entries)-1].Balance)
}

// A first period that is exactly one anchored month is not a stub, however the
// disbursal date is written: the first installment is a normal one.
func TestLoanAmortizationWholeFirstMonthIsNotAStub(t *testing.T) {
	start := time.Date(2024, 4, 5, 0, 0, 0, 0, time.UTC)
	disbursal := time.Date(2024, 3, 5, 0, 0, 0, 0, time.UTC)

	_, entries := loanAmortization(loanTerms{
		principal:     money.FromFloat(1000),
		annualRateBps: 1200,
		tenureMonths:  12,
		firstDueDate:  start,
		disbursalDate: &disbursal,
	}, nil)

	require.Len(t, entries, 12)
	assert.Equal(t, money.FromFloat(10), entries[0].Interest)
	assert.Equal(t, money.FromFloat(79), entries[0].Principal)
}

// A broken first period long enough that its interest exceeds a whole-month
// installment cannot leave negative principal: that installment pays interest
// only, and the final installment absorbs the balance it leaves.
func TestLoanAmortizationStubInterestBeyondInstallmentPaysNoPrincipal(t *testing.T) {
	start := time.Date(2025, 1, 5, 0, 0, 0, 0, time.UTC)
	disbursal := time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC)

	emi, entries := loanAmortization(loanTerms{
		principal:     money.FromFloat(1200),
		annualRateBps: 1200,
		tenureMonths:  12,
		firstDueDate:  start,
		disbursalDate: &disbursal,
	}, nil)

	require.Len(t, entries, 12)
	// A full year of interest on the whole balance, which the monthly
	// installment does not cover.
	assert.Greater(t, entries[0].Interest, emi)
	assert.Zero(t, entries[0].Principal)
	assert.Equal(t, entries[0].Interest, entries[0].Amount)
	assert.Equal(t, money.FromFloat(1200), entries[1].Balance+entries[1].Principal)

	// The balance it leaves is still repaid in full by the end of the tenure.
	var principalSum money.Amount
	for _, e := range entries {
		principalSum += e.Principal
	}
	assert.Equal(t, money.FromFloat(1200), principalSum)
	assert.Zero(t, entries[len(entries)-1].Balance)
}

// A balance transfer adds principal on its date, and the installments still due
// after it are recast over the larger balance: a different EMI from that
// installment on, with the ones before it untouched.
func TestLoanAmortizationRecastsAfterTransfer(t *testing.T) {
	start := time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)
	// The EMI the table starts with, before any transfer: the returned EMI is
	// the one in force for the last segment, which the transfer changes.
	originalEMI := money.FromFloat(107)
	emi, entries := loanAmortization(loanTerms{
		principal:     money.FromFloat(1200),
		annualRateBps: 1200,
		tenureMonths:  12,
		firstDueDate:  start,
	}, []principalAdjustment{{date: time.Date(2024, 3, 20, 0, 0, 0, 0, time.UTC), amount: money.FromFloat(600)}})

	require.Len(t, entries, 12)

	// Installments 1-3 are due on or before the transfer date, so they keep the
	// original EMI and are not marked recast.
	for i := range 3 {
		assert.Equal(t, originalEMI, entries[i].Amount)
		assert.False(t, entries[i].Recast)
	}
	// The rest amortize the balance plus the transferred 600 over the nine
	// installments left, which raises the installment from 107.00 to 177.00.
	assert.Equal(t, "2024-04-10", entries[3].DueDate.Format("2006-01-02"))
	assert.Equal(t, money.FromFloat(177), emi)
	assert.Equal(t, emi, entries[3].Amount)
	assert.Greater(t, emi, originalEMI)
	for _, e := range entries[3:] {
		assert.True(t, e.Recast)
	}

	// The whole principal plus what was transferred is repaid.
	var principalSum money.Amount
	for _, e := range entries {
		principalSum += e.Principal
	}
	assert.Equal(t, money.FromFloat(1800), principalSum)
	assert.Zero(t, entries[len(entries)-1].Balance)
}

// A transfer dated on an installment's due date leaves that installment as the
// lender had it: only the ones due strictly after it are recast.
func TestLoanAmortizationTransferOnDueDateLeavesThatInstallment(t *testing.T) {
	start := time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)
	originalEMI := money.FromFloat(107)
	_, entries := loanAmortization(loanTerms{
		principal:     money.FromFloat(1200),
		annualRateBps: 1200,
		tenureMonths:  12,
		firstDueDate:  start,
	}, []principalAdjustment{
		{date: time.Date(2024, 3, 10, 0, 0, 0, 0, time.UTC), amount: money.FromFloat(600)},
	})

	assert.Equal(t, originalEMI, entries[2].Amount)
	assert.False(t, entries[2].Recast)
	assert.True(t, entries[3].Recast)
	assert.Greater(t, entries[3].Amount, originalEMI)
}

// A loan with no remaining installments (the tenure is spent) has nothing to
// recast: the adjustment is simply not applied, so the table still repays its
// own principal exactly.
func TestLoanAmortizationTransferAfterFinalInstallmentIsIgnored(t *testing.T) {
	start := time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)
	_, entries := loanAmortization(loanTerms{
		principal:     money.FromFloat(1200),
		annualRateBps: 1200,
		tenureMonths:  12,
		firstDueDate:  start,
	}, []principalAdjustment{{date: time.Date(2025, 6, 10, 0, 0, 0, 0, time.UTC), amount: money.FromFloat(600)}})

	assert.Zero(t, entries[len(entries)-1].Balance)
	for _, e := range entries {
		assert.False(t, e.Recast)
	}
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
	expectLoanScheduleRows(mock, accountID, userID,
		[]any{scheduleID, accountID, money.FromFloat(1000), money.FromFloat(0), 1200, 12,
			time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC), nil, created, created})
	expectNoLoanTransfers(mock, accountID, userID)
	expectLoanDisbursementCredit(mock, accountID, userID)
	mock.ExpectQuery("FROM loan_attachments").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "amount", "date", "type"}).
			AddRow(paidTxn, money.FromFloat(88.85), created, "debit").
			AddRow(refundTxn, money.FromFloat(10), created, "credit"))

	req, _ := http.NewRequest(http.MethodGet, "/accounts/"+accountID.String()+"/loan-schedule", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var detail models.LoanScheduleDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
	require.NotNil(t, detail.Schedule)
	assert.Equal(t, money.FromFloat(89), detail.EMI)
	assert.Len(t, detail.Entries, 12)
	// The transfers list is always an array, even for a loan that took part in
	// none: the client renders it without a null check.
	assert.NotNil(t, detail.Transfers)
	assert.Empty(t, detail.Transfers)
	assert.Contains(t, w.Body.String(), `"transfers":[]`)

	// The single debit covers the first installment; the refund reduces what was
	// paid without covering another one.
	assert.Equal(t, 1, detail.PaidInstallments)
	assert.True(t, detail.Entries[0].Paid)
	require.NotNil(t, detail.Entries[0].TransactionID)
	assert.Equal(t, paidTxn, *detail.Entries[0].TransactionID)
	assert.False(t, detail.Entries[1].Paid)
	assert.Equal(t, money.FromFloat(78.85), detail.PaidAmount) // 88.85 - 10
	assert.Equal(t, money.FromFloat(79), detail.PrincipalPaid)
	assert.Equal(t, money.FromFloat(10), detail.InterestPaid)
	assert.Equal(t, money.FromFloat(921), detail.OutstandingPrincipal)
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
		{"negative fee", base(map[string]any{"processingFee": -1}), "processingFee must not be negative"},
		{"negative rate", base(map[string]any{"annualRateBps": -1}), "annualRateBps must not be negative"},
		{"zero tenure", base(map[string]any{"tenureMonths": 0}), "tenureMonths is required"},
		{"long tenure", base(map[string]any{"tenureMonths": 601}), "tenureMonths must be between 1 and 600"},
		{"bad date", base(map[string]any{"startDate": "01-04-2024"}), "invalid date"},
		{"bad disbursal date", base(map[string]any{"disbursalDate": "20-02-2024"}), "invalid date"},
		{"disbursal on first installment", base(map[string]any{"disbursalDate": "2024-04-01"}), "disbursalDate must be before"},
		{"disbursal after first installment", base(map[string]any{"disbursalDate": "2024-05-01"}), "disbursalDate must be before"},
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
		WithArgs(accountID, userID, money.FromFloat(1000), money.FromFloat(50), 1200, 12, start, (*time.Time)(nil)).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	expectLoanScheduleRows(mock, accountID, userID,
		[]any{uuid.New(), accountID, money.FromFloat(1000), money.FromFloat(50), 1200, 12, start, nil, created, created})
	expectNoLoanTransfers(mock, accountID, userID)
	expectLoanDisbursementCredit(mock, accountID, userID)
	mock.ExpectQuery("FROM loan_attachments").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "amount", "date", "type"}))

	body := map[string]any{
		"principal":     1000,
		"processingFee": 50,
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
	// The fee is stored for reference but not amortized: the EMI is the one for
	// the whole 1000.00 principal, and the table repays it in full.
	assert.Equal(t, money.FromFloat(50), detail.Schedule.ProcessingFee)
	assert.Equal(t, money.FromFloat(89), detail.EMI)
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
