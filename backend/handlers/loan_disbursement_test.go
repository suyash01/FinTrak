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

// Disbursement verification: the bank credit that released a loan, reconciled
// against the disbursement its schedule implies
// (PUT/DELETE /accounts/:id/loan-disbursement).

// expectLoanDisbursementCredit matches the credit read of a loan, returning the
// linked transaction (none when the arguments are empty).
func expectLoanDisbursementCredit(mock pgxmock.PgxPoolIface, accountID, userID any, rows ...[]any) {
	mock.ExpectQuery("FROM loan_disbursements").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"transaction_id", "amount"}).AddRows(rows...))
}

// linkFixture is one loan with a schedule, ready for a link request.
type linkFixture struct {
	mock     pgxmock.PgxPoolIface
	router   http.Handler
	userID   uuid.UUID
	loanID   uuid.UUID
	txnID    uuid.UUID
	created  time.Time
	start    time.Time
	disburse *time.Time
}

// newLinkFixture builds a router over a mocked pool whose loan is a 1,000.00
// 12-month loan at 12% with a 50.00 reference fee, disbursed 2024-03-15.
func newLinkFixture(t *testing.T) *linkFixture {
	t.Helper()
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	t.Cleanup(mock.Close)
	srv := newTestServer(mock)
	return &linkFixture{
		mock:    mock,
		router:  newLoanScheduleTestRouter(srv),
		userID:  testUserID(),
		loanID:  uuid.New(),
		txnID:   uuid.New(),
		created: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		start:   time.Date(2024, 4, 5, 0, 0, 0, 0, time.UTC),
	}
}

// expectLoanGuard matches the route's loan-account lookup.
func (f *linkFixture) expectLoanGuard() {
	f.mock.ExpectQuery("SELECT name, account_type_id FROM accounts").
		WithArgs(f.loanID, f.userID).
		WillReturnRows(f.mock.NewRows([]string{"name", "account_type_id"}).AddRow("Car Loan", "loan"))
}

// expectScheduleRows matches the loan's own schedule row.
func (f *linkFixture) expectScheduleRows() {
	expectLoanScheduleRows(f.mock, f.loanID, f.userID,
		[]any{uuid.New(), f.loanID, money.FromFloat(1000), money.FromFloat(50), 1200, 12,
			f.start, f.disburse, f.created, f.created})
}

// expectDetailReload matches one full detail load: schedule, transfers, the
// linked credit, and the attached EMI payments.
func (f *linkFixture) expectDetailReload(creditRows ...[]any) {
	f.expectScheduleRows()
	expectNoLoanTransfers(f.mock, f.loanID, f.userID)
	expectLoanDisbursementCredit(f.mock, f.loanID, f.userID, creditRows...)
	expectLoanPayments(f.mock, f.loanID, f.userID)
}

// expectTransactionCheck matches the credit transaction lookup. txnType and
// accountType describe the transaction being linked.
func (f *linkFixture) expectTransactionCheck(txnType, accountType string) {
	f.mock.ExpectQuery("FROM transactions t").
		WithArgs(f.txnID).
		WillReturnRows(f.mock.NewRows([]string{"user_id", "type", "account_type_id"}).
			AddRow(f.userID, txnType, accountType))
}

func (f *linkFixture) link(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	req := jsonRequest(t, http.MethodPut, "/accounts/"+f.loanID.String()+"/loan-disbursement",
		map[string]any{"transactionId": f.txnID})
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

// The point of the feature: the credit that actually arrived is compared with
// what the schedule implies, and a match is reported as verified.
func TestLinkLoanDisbursementVerifiesMatchingCredit(t *testing.T) {
	f := newLinkFixture(t)

	f.expectLoanGuard()
	f.mock.ExpectQuery("SELECT EXISTS").
		WithArgs(f.loanID, f.userID).
		WillReturnRows(f.mock.NewRows([]string{"exists"}).AddRow(true))
	f.expectTransactionCheck("credit", "bank")
	f.mock.ExpectQuery("SELECT loan_account_id FROM loan_disbursements").
		WithArgs(f.txnID, f.userID).
		WillReturnError(pgx.ErrNoRows)
	// 1,000.00 sanctioned less the 50.00 fee is what should have arrived.
	// The insert is guarded: it writes only while the transaction is not an EMI
	// payment, so there is no separate attachment pre-check to mock.
	f.mock.ExpectExec("INSERT INTO loan_disbursements").
		WithArgs(f.loanID, f.txnID, f.userID).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	f.expectDetailReload([]any{f.txnID, money.FromFloat(950)})

	w := f.link(t)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var detail models.LoanScheduleDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
	require.NotNil(t, detail.Disbursement)
	assert.Equal(t, money.FromFloat(1000), detail.Disbursement.Sanctioned)
	assert.Equal(t, money.FromFloat(50), detail.Disbursement.ProcessingFee)
	assert.Zero(t, detail.Disbursement.PaidOut)
	assert.Equal(t, money.FromFloat(950), detail.Disbursement.Net)
	require.NotNil(t, detail.Disbursement.CreditTransactionID)
	assert.Equal(t, f.txnID, *detail.Disbursement.CreditTransactionID)
	assert.True(t, detail.Disbursement.Verified)
	assert.Zero(t, detail.Disbursement.Difference)
	assert.NoError(t, f.mock.ExpectationsWereMet())
}

// A credit that does not match is the thing the user needs to see, so it is
// reported rather than rejected.
func TestLinkLoanDisbursementReportsMismatch(t *testing.T) {
	f := newLinkFixture(t)

	f.expectLoanGuard()
	f.mock.ExpectQuery("SELECT EXISTS").
		WithArgs(f.loanID, f.userID).
		WillReturnRows(f.mock.NewRows([]string{"exists"}).AddRow(true))
	f.expectTransactionCheck("credit", "bank")
	f.mock.ExpectQuery("SELECT loan_account_id FROM loan_disbursements").
		WithArgs(f.txnID, f.userID).
		WillReturnError(pgx.ErrNoRows)
	f.mock.ExpectExec("INSERT INTO loan_disbursements").
		WithArgs(f.loanID, f.txnID, f.userID).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	// 942.50 arrived against the 950.00 the schedule implies: the fee was
	// charged twice, or the lender withheld something else.
	f.expectDetailReload([]any{f.txnID, money.FromFloat(942.50)})

	w := f.link(t)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var detail models.LoanScheduleDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
	require.NotNil(t, detail.Disbursement)
	assert.False(t, detail.Disbursement.Verified)
	assert.Equal(t, money.FromFloat(-7.50), detail.Disbursement.Difference)
	assert.NoError(t, f.mock.ExpectationsWereMet())
}

func TestLinkLoanDisbursementRejections(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *linkFixture)
		want  string
		code  int
	}{
		{
			name: "loan has no schedule",
			setup: func(f *linkFixture) {
				f.expectLoanGuard()
				f.mock.ExpectQuery("SELECT EXISTS").WithArgs(f.loanID, f.userID).
					WillReturnRows(f.mock.NewRows([]string{"exists"}).AddRow(false))
			},
			want: "no amortization schedule",
			code: http.StatusBadRequest,
		},
		{
			name: "transaction is missing",
			setup: func(f *linkFixture) {
				f.expectLoanGuard()
				f.mock.ExpectQuery("SELECT EXISTS").WithArgs(f.loanID, f.userID).
					WillReturnRows(f.mock.NewRows([]string{"exists"}).AddRow(true))
				f.mock.ExpectQuery("FROM transactions t").WithArgs(f.txnID).WillReturnError(pgx.ErrNoRows)
			},
			want: "transaction not found",
			code: http.StatusNotFound,
		},
		{
			name: "transaction belongs to another user",
			setup: func(f *linkFixture) {
				f.expectLoanGuard()
				f.mock.ExpectQuery("SELECT EXISTS").WithArgs(f.loanID, f.userID).
					WillReturnRows(f.mock.NewRows([]string{"exists"}).AddRow(true))
				f.mock.ExpectQuery("FROM transactions t").WithArgs(f.txnID).
					WillReturnRows(f.mock.NewRows([]string{"user_id", "type", "account_type_id"}).
						AddRow(uuid.New(), "credit", "bank"))
			},
			want: "forbidden",
			code: http.StatusForbidden,
		},
		{
			name: "transaction lives on a loan account",
			setup: func(f *linkFixture) {
				f.expectLoanGuard()
				f.mock.ExpectQuery("SELECT EXISTS").WithArgs(f.loanID, f.userID).
					WillReturnRows(f.mock.NewRows([]string{"exists"}).AddRow(true))
				f.expectTransactionCheck("credit", "loan")
			},
			want: "loan account holds no transactions",
			code: http.StatusBadRequest,
		},
		{
			name: "transaction is a debit",
			setup: func(f *linkFixture) {
				f.expectLoanGuard()
				f.mock.ExpectQuery("SELECT EXISTS").WithArgs(f.loanID, f.userID).
					WillReturnRows(f.mock.NewRows([]string{"exists"}).AddRow(true))
				f.expectTransactionCheck("debit", "bank")
			},
			want: "must be a credit transaction",
			code: http.StatusBadRequest,
		},
		{
			name: "transaction is already an EMI payment",
			setup: func(f *linkFixture) {
				f.expectLoanGuard()
				f.mock.ExpectQuery("SELECT EXISTS").WithArgs(f.loanID, f.userID).
					WillReturnRows(f.mock.NewRows([]string{"exists"}).AddRow(true))
				f.expectTransactionCheck("credit", "bank")
				f.mock.ExpectQuery("SELECT loan_account_id FROM loan_disbursements").
					WithArgs(f.txnID, f.userID).
					WillReturnError(pgx.ErrNoRows)
				// The exclusivity guard is part of the write: the statement
				// inserts nothing when the transaction is an EMI payment, and
				// that zero row count is this 409.
				f.mock.ExpectExec("INSERT INTO loan_disbursements").
					WithArgs(f.loanID, f.txnID, f.userID).
					WillReturnResult(pgxmock.NewResult("INSERT", 0))
			},
			want: "already an EMI payment",
			code: http.StatusConflict,
		},
		{
			name: "transaction is another loan's disbursement",
			setup: func(f *linkFixture) {
				f.expectLoanGuard()
				f.mock.ExpectQuery("SELECT EXISTS").WithArgs(f.loanID, f.userID).
					WillReturnRows(f.mock.NewRows([]string{"exists"}).AddRow(true))
				f.expectTransactionCheck("credit", "bank")
				f.mock.ExpectQuery("SELECT loan_account_id FROM loan_disbursements").
					WithArgs(f.txnID, f.userID).
					WillReturnRows(f.mock.NewRows([]string{"loan_account_id"}).AddRow(uuid.New()))
			},
			want: "another loan",
			code: http.StatusConflict,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newLinkFixture(t)
			tc.setup(f)

			w := f.link(t)

			assert.Equal(t, tc.code, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), tc.want)
			assert.NoError(t, f.mock.ExpectationsWereMet())
		})
	}
}

func TestLinkLoanDisbursementBadRequest(t *testing.T) {
	srv, _ := newMockServer(t)
	r := newLoanScheduleTestRouter(srv)

	req := jsonRequest(t, http.MethodPut, "/accounts/nope/loan-disbursement", map[string]any{"transactionId": uuid.NewString()})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUnlinkLoanDisbursement(t *testing.T) {
	for _, deleted := range []int64{1, 0} {
		t.Run("removes "+string(rune('0'+deleted)), func(t *testing.T) {
			mock, err := pgxmock.NewPool()
			require.NoError(t, err)
			defer mock.Close()
			srv := newTestServer(mock)
			r := newLoanScheduleTestRouter(srv)

			loanID := uuid.New()
			mock.ExpectQuery("SELECT name, account_type_id FROM accounts").
				WithArgs(loanID, testUserID()).
				WillReturnRows(mock.NewRows([]string{"name", "account_type_id"}).AddRow("Car Loan", "loan"))
			mock.ExpectExec("DELETE FROM loan_disbursements").
				WithArgs(loanID, testUserID()).
				WillReturnResult(pgxmock.NewResult("DELETE", deleted))

			req, _ := http.NewRequest(http.MethodDelete, "/accounts/"+loanID.String()+"/loan-disbursement", nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)
			assert.JSONEq(t, `{"deleted":`+string(rune('0'+deleted))+`}`, w.Body.String())
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
