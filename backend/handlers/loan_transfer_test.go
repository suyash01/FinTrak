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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Balance transfers between two loan accounts
// (POST /accounts/:id/loan-transfer, DELETE /accounts/:id/loan-transfer/:transferId).

// transferTerms is the schedule both fixtures generate their tables from: a
// 12-month 1,200.00 loan at 12% p.a., first installment 2024-01-10.
var transferStart = time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)

// transferScheduleRow is a loan_schedules row for the fixture terms.
func transferScheduleRow(id, accountID uuid.UUID) []any {
	return []any{id, accountID, money.FromFloat(1200), money.FromFloat(0), 1200, 12,
		transferStart, nil, transferStart, transferStart}
}

// transferRowJSON is a loan_transfers row as the loader reads it. The principal
// is the part of the amount that was principal rather than accrued interest.
func transferRowJSON(id, fromID, toID uuid.UUID, amount, principal money.Amount, date time.Time, mode string) []any {
	return []any{id, fromID, "Car Loan", toID, "Home Loan", amount, principal, date, mode, date}
}

// transferSecondPayment is when the source fixture's second EMI was paid: the
// date a later settlement accrues interest from.
var transferSecondPayment = time.Date(2024, 2, 10, 0, 0, 0, 0, time.UTC)

// expectLoanScheduleRowsFor matches a loan's own schedule row with the given
// principal and rate, so a takeover target can have terms of its own.
func expectLoanScheduleRowsFor(mock pgxmock.PgxPoolIface, accountID, userID any, principal money.Amount, bps, months int) {
	expectLoanScheduleRows(mock, accountID, userID,
		[]any{uuid.New(), accountID, principal, money.FromFloat(0), bps, months,
			transferStart, nil, transferStart, transferStart})
}

// expectLoanPayments matches the attached-EMI read of a loan, oldest first.
func expectLoanPayments(mock pgxmock.PgxPoolIface, accountID, userID any, rows ...[]any) {
	mock.ExpectQuery("FROM loan_attachments").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "amount", "date", "type"}).AddRows(rows...))
}

// The whole point of the feature: the source's unpaid installments are void and
// nothing is outstanding on it, while the target's installments still due are
// recast over the balance it absorbed.
func TestTransferLoanBalanceRecastsTargetAndSettlesSource(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	srv := newTestServer(mock)
	r := newLoanScheduleTestRouter(srv)

	userID := testUserID()
	sourceID, targetID := uuid.New(), uuid.New()
	transferID := uuid.New()
	paidTxn1, paidTxn2 := uuid.New(), uuid.New()
	transferDate := time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)

	// The source has paid its first two installments, so 1,009.05 is left of its
	// 1,200.00 principal, plus the interest accrued since the second was paid
	// (2024-02-10 to the 2024-03-15 transfer date).
	mock.ExpectQuery("SELECT name, account_type_id FROM accounts").
		WithArgs(sourceID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"name", "account_type_id"}).AddRow("Car Loan", "loan"))
	expectLoanScheduleRows(mock, sourceID, userID, transferScheduleRow(uuid.New(), sourceID))
	expectNoLoanTransfers(mock, sourceID, userID)
	expectLoanDisbursementCredit(mock, sourceID, userID)
	expectLoanPayments(mock, sourceID, userID,
		[]any{paidTxn1, money.FromFloat(107), transferStart, "debit"},
		[]any{paidTxn2, money.FromFloat(107), transferSecondPayment, "debit"})

	// The target is an open loan account of the caller's.
	mock.ExpectQuery("SELECT user_id, name, account_type_id, closed FROM accounts").
		WithArgs(targetID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "name", "account_type_id", "closed"}).
			AddRow(userID, "Home Loan", "loan", false))
	expectLoanScheduleRows(mock, targetID, userID, transferScheduleRow(uuid.New(), targetID))
	expectNoLoanTransfers(mock, targetID, userID)
	expectLoanDisbursementCredit(mock, targetID, userID)
	expectLoanPayments(mock, targetID, userID)

	// The transfer row is the only write; the target already had a schedule.
	// The row's id is minted by the handler, so only the other columns are
	// pinned.
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO loan_transfers").
		WithArgs(pgxmock.AnyArg(), userID, sourceID, targetID, money.FromFloat(1020.49), money.FromFloat(1009.05), transferDate, models.LoanTransferRecast).
		WillReturnRows(pgxmock.NewRows([]string{"created_at"}).AddRow(transferDate))
	mock.ExpectCommit()

	// Both loans reload: the source now carries the transfer that settled it,
	// the target the one it absorbed.
	expectLoanScheduleRows(mock, sourceID, userID, transferScheduleRow(uuid.New(), sourceID))
	mock.ExpectQuery("FROM loan_transfers").
		WithArgs(sourceID, userID).
		WillReturnRows(pgxmock.NewRows(loanTransferCols).
			AddRow(transferRowJSON(transferID, sourceID, targetID, money.FromFloat(1020.49), money.FromFloat(1009.05), transferDate, models.LoanTransferRecast)...))
	expectLoanDisbursementCredit(mock, sourceID, userID)
	expectLoanPayments(mock, sourceID, userID,
		[]any{paidTxn1, money.FromFloat(107), transferStart, "debit"},
		[]any{paidTxn2, money.FromFloat(107), transferSecondPayment, "debit"})
	expectLoanScheduleRows(mock, targetID, userID, transferScheduleRow(uuid.New(), targetID))
	mock.ExpectQuery("FROM loan_transfers").
		WithArgs(targetID, userID).
		WillReturnRows(pgxmock.NewRows(loanTransferCols).
			AddRow(transferRowJSON(transferID, sourceID, targetID, money.FromFloat(1020.49), money.FromFloat(1009.05), transferDate, models.LoanTransferRecast)...))
	expectLoanDisbursementCredit(mock, targetID, userID)
	expectLoanPayments(mock, targetID, userID)

	body := map[string]any{"toLoanAccountId": targetID.String(), "transferDate": "2024-03-15"}
	req := jsonRequest(t, http.MethodPost, "/accounts/"+sourceID.String()+"/loan-transfer", body)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var res models.LoanTransferResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Equal(t, money.FromFloat(1020.49), res.Transfer.Amount)
	assert.Equal(t, money.FromFloat(1009.05), res.Transfer.Principal)
	assert.Equal(t, money.FromFloat(11.44), res.Transfer.AccruedInterest)
	assert.Equal(t, sourceID, res.Transfer.FromLoanAccountID)
	assert.Equal(t, targetID, res.Transfer.ToLoanAccountID)
	assert.Equal(t, "Home Loan", res.Transfer.ToLoanAccountName)

	// Source: settled, so its two paid installments stand and the ten it still
	// owed are void and no longer owe anything.
	require.NotNil(t, res.Source.SettledOn)
	assert.True(t, res.Source.Completed)
	assert.Zero(t, res.Source.OutstandingPrincipal)
	assert.Equal(t, 2, res.Source.PaidInstallments)
	assert.True(t, res.Source.Entries[0].Paid)
	assert.False(t, res.Source.Entries[0].Cancelled)
	assert.True(t, res.Source.Entries[2].Cancelled)
	assert.Nil(t, res.Source.NextDueDate)
	assert.Nil(t, res.Source.Entries[2].TransactionID)

	// Target: the installments still due are recast over 1,200.00 plus the
	// 1,020.49 payoff that moved in, so its EMI rises above the 107.00 it started
	// at.
	assert.Nil(t, res.Target.SettledOn)
	assert.Equal(t, money.FromFloat(1200), res.Target.Schedule.Principal)
	assert.Equal(t, money.FromFloat(2220.49), res.Target.OutstandingPrincipal)
	require.Len(t, res.Target.Entries, 12)
	assert.False(t, res.Target.Entries[0].Recast)
	assert.True(t, res.Target.Entries[3].Recast)
	assert.Greater(t, res.Target.Entries[3].Amount, res.Target.Entries[0].Amount)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A target with no schedule has nothing to recast, so the transferred amount
// starts one from the request's terms — and the amount must not be counted
// twice, since it is that schedule's principal.
func TestTransferLoanBalanceCreatesTargetScheduleWhenTargetHasNone(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	srv := newTestServer(mock)
	r := newLoanScheduleTestRouter(srv)

	userID := testUserID()
	sourceID, targetID := uuid.New(), uuid.New()
	transferID := uuid.New()
	transferDate := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery("SELECT name, account_type_id FROM accounts").
		WithArgs(sourceID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"name", "account_type_id"}).AddRow("Car Loan", "loan"))
	expectLoanScheduleRows(mock, sourceID, userID, transferScheduleRow(uuid.New(), sourceID))
	expectNoLoanTransfers(mock, sourceID, userID)
	expectLoanDisbursementCredit(mock, sourceID, userID)
	expectLoanPayments(mock, sourceID, userID)

	mock.ExpectQuery("SELECT user_id, name, account_type_id, closed FROM accounts").
		WithArgs(targetID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "name", "account_type_id", "closed"}).
			AddRow(userID, "Home Loan", "loan", false))
	// The target has no terms yet, which ends its detail load here.
	mock.ExpectQuery("FROM loan_schedules").
		WithArgs(targetID, userID).
		WillReturnError(pgx.ErrNoRows)

	// The write records the transfer and starts the target's schedule, whose
	// principal is the amount that moved in.
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO loan_transfers").
		WithArgs(pgxmock.AnyArg(), userID, sourceID, targetID, money.FromFloat(1269.60), money.FromFloat(1200), transferDate, models.LoanTransferOpens).
		WillReturnRows(pgxmock.NewRows([]string{"created_at"}).AddRow(transferDate))
	mock.ExpectExec("INSERT INTO loan_schedules").
		WithArgs(targetID, userID, money.FromFloat(1269.60), 900, 24, time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC)).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	expectLoanScheduleRows(mock, sourceID, userID, transferScheduleRow(uuid.New(), sourceID))
	mock.ExpectQuery("FROM loan_transfers").
		WithArgs(sourceID, userID).
		WillReturnRows(pgxmock.NewRows(loanTransferCols).
			AddRow(transferRowJSON(transferID, sourceID, targetID, money.FromFloat(1269.60), money.FromFloat(1200), transferDate, models.LoanTransferOpens)...))
	expectLoanDisbursementCredit(mock, sourceID, userID)
	expectLoanPayments(mock, sourceID, userID)
	expectLoanScheduleRows(mock, targetID, userID,
		[]any{uuid.New(), targetID, money.FromFloat(1269.60), money.FromFloat(0), 900, 24,
			time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC), nil, transferDate, transferDate})
	mock.ExpectQuery("FROM loan_transfers").
		WithArgs(targetID, userID).
		WillReturnRows(pgxmock.NewRows(loanTransferCols).
			AddRow(transferRowJSON(transferID, sourceID, targetID, money.FromFloat(1269.60), money.FromFloat(1200), transferDate, models.LoanTransferOpens)...))
	expectLoanDisbursementCredit(mock, targetID, userID)
	expectLoanPayments(mock, targetID, userID)

	body := map[string]any{
		"toLoanAccountId":     targetID.String(),
		"transferDate":        "2024-06-01",
		"targetAnnualRateBps": 900,
		"targetTenureMonths":  24,
		"targetStartDate":     "2024-07-01",
	}
	req := jsonRequest(t, http.MethodPost, "/accounts/"+sourceID.String()+"/loan-transfer", body)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, 200, w.Code)

	var res models.LoanTransferResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	// The target amortizes the transferred balance exactly once.
	assert.Equal(t, money.FromFloat(1269.60), res.Target.Schedule.Principal)
	assert.Equal(t, money.FromFloat(1269.60), res.Target.OutstandingPrincipal)
	require.Len(t, res.Target.Entries, 24)
	assert.False(t, res.Target.Entries[0].Recast)
	assert.Equal(t, money.FromFloat(58), res.Target.Entries[0].Amount)
	assert.True(t, res.Source.Completed)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Every way a transfer can be refused before anything is written.
func TestTransferLoanBalanceRejections(t *testing.T) {
	userID := testUserID()
	sourceID, targetID := uuid.New(), uuid.New()

	// The source detail each case starts from: a 1,200.00 loan, optionally with
	// its two first installments paid (leaving 1,009.81 outstanding) and
	// optionally already settled.
	sourceFixture := func(mock pgxmock.PgxPoolIface, paid int, settled bool) {
		mock.ExpectQuery("SELECT name, account_type_id FROM accounts").
			WithArgs(sourceID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"name", "account_type_id"}).AddRow("Car Loan", "loan"))
		expectLoanScheduleRows(mock, sourceID, userID, transferScheduleRow(uuid.New(), sourceID))
		transfers := pgxmock.NewRows(loanTransferCols)
		if settled {
			transfers.AddRow(transferRowJSON(uuid.New(), sourceID, targetID, money.FromFloat(1009.81), money.FromFloat(1009.81),
				time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC), models.LoanTransferRecast)...)
		}
		mock.ExpectQuery("FROM loan_transfers").WithArgs(sourceID, userID).WillReturnRows(transfers)
		payments := pgxmock.NewRows([]string{"id", "amount", "date", "type"})
		for range paid {
			payments.AddRow(uuid.New(), money.FromFloat(107), transferStart, "debit")
		}
		expectLoanDisbursementCredit(mock, sourceID, userID)
		mock.ExpectQuery("FROM loan_attachments").WithArgs(sourceID, userID).WillReturnRows(payments)
	}

	targetFixture := func(mock pgxmock.PgxPoolIface, accountType string, closed bool) {
		mock.ExpectQuery("SELECT user_id, name, account_type_id, closed FROM accounts").
			WithArgs(targetID).
			WillReturnRows(pgxmock.NewRows([]string{"user_id", "name", "account_type_id", "closed"}).
				AddRow(userID, "Home Loan", accountType, closed))
	}

	cases := []struct {
		name  string
		body  map[string]any
		setup func(mock pgxmock.PgxPoolIface)
		want  string
		code  int
	}{
		{
			name: "source has no schedule",
			body: map[string]any{"toLoanAccountId": targetID.String(), "transferDate": "2024-03-15"},
			setup: func(mock pgxmock.PgxPoolIface) {
				mock.ExpectQuery("SELECT name, account_type_id FROM accounts").
					WithArgs(sourceID, userID).
					WillReturnRows(pgxmock.NewRows([]string{"name", "account_type_id"}).AddRow("Car Loan", "loan"))
				mock.ExpectQuery("FROM loan_schedules").WithArgs(sourceID, userID).WillReturnError(pgx.ErrNoRows)
			},
			want: "no amortization schedule",
			code: http.StatusBadRequest,
		},
		{
			name: "source already settled",
			body: map[string]any{"toLoanAccountId": targetID.String(), "transferDate": "2024-03-15"},
			setup: func(mock pgxmock.PgxPoolIface) {
				sourceFixture(mock, 0, true)
			},
			want: "already settled",
			code: http.StatusBadRequest,
		},
		{
			name: "source has nothing outstanding",
			body: map[string]any{"toLoanAccountId": targetID.String(), "transferDate": "2024-03-15"},
			setup: func(mock pgxmock.PgxPoolIface) {
				sourceFixture(mock, 12, false)
			},
			want: "nothing left to transfer",
			code: http.StatusBadRequest,
		},
		{
			name: "target is the source",
			body: map[string]any{"toLoanAccountId": sourceID.String(), "transferDate": "2024-03-15"},
			setup: func(mock pgxmock.PgxPoolIface) {
				// The request is refused before the source is even read.
			},
			want: "different account",
			code: http.StatusBadRequest,
		},
		{
			name: "target is missing",
			body: map[string]any{"toLoanAccountId": targetID.String(), "transferDate": "2024-03-15"},
			setup: func(mock pgxmock.PgxPoolIface) {
				sourceFixture(mock, 2, false)
				mock.ExpectQuery("SELECT user_id, name, account_type_id, closed FROM accounts").
					WithArgs(targetID).
					WillReturnError(pgx.ErrNoRows)
			},
			want: "target loan account not found",
			code: http.StatusNotFound,
		},
		{
			name: "target belongs to another user",
			body: map[string]any{"toLoanAccountId": targetID.String(), "transferDate": "2024-03-15"},
			setup: func(mock pgxmock.PgxPoolIface) {
				sourceFixture(mock, 2, false)
				mock.ExpectQuery("SELECT user_id, name, account_type_id, closed FROM accounts").
					WithArgs(targetID).
					WillReturnRows(pgxmock.NewRows([]string{"user_id", "name", "account_type_id", "closed"}).
						AddRow(uuid.New(), "Home Loan", "loan", false))
			},
			want: "forbidden",
			code: http.StatusForbidden,
		},
		{
			name: "target is not a loan account",
			body: map[string]any{"toLoanAccountId": targetID.String(), "transferDate": "2024-03-15"},
			setup: func(mock pgxmock.PgxPoolIface) {
				sourceFixture(mock, 2, false)
				targetFixture(mock, "bank", false)
			},
			want: "not a Loan / EMI account",
			code: http.StatusBadRequest,
		},
		{
			name: "target is closed",
			body: map[string]any{"toLoanAccountId": targetID.String(), "transferDate": "2024-03-15"},
			setup: func(mock pgxmock.PgxPoolIface) {
				sourceFixture(mock, 2, false)
				targetFixture(mock, "loan", true)
			},
			want: "target loan account is closed",
			code: http.StatusBadRequest,
		},
		{
			name: "target has no installment left to absorb the balance",
			body: map[string]any{"toLoanAccountId": targetID.String(), "transferDate": "2024-03-15"},
			setup: func(mock pgxmock.PgxPoolIface) {
				sourceFixture(mock, 2, false)
				targetFixture(mock, "loan", false)
				expectLoanScheduleRows(mock, targetID, userID, transferScheduleRow(uuid.New(), targetID))
				expectNoLoanTransfers(mock, targetID, userID)
				expectLoanDisbursementCredit(mock, targetID, userID)
				// Every installment of the target is already paid.
				rows := pgxmock.NewRows([]string{"id", "amount", "date", "type"})
				for range 12 {
					rows.AddRow(uuid.New(), money.FromFloat(107), transferStart, "debit")
				}
				mock.ExpectQuery("FROM loan_attachments").WithArgs(targetID, userID).WillReturnRows(rows)
			},
			want: "no unpaid installment due after the transfer date",
			code: http.StatusBadRequest,
		},
		{
			name: "takeover of a target with no schedule",
			body: map[string]any{"toLoanAccountId": targetID.String(), "transferDate": "2024-03-15", "mode": "takeover"},
			setup: func(mock pgxmock.PgxPoolIface) {
				sourceFixture(mock, 2, false)
				targetFixture(mock, "loan", false)
				mock.ExpectQuery("FROM loan_schedules").WithArgs(targetID, userID).WillReturnError(pgx.ErrNoRows)
			},
			want: "no schedule to take the balance out of",
			code: http.StatusBadRequest,
		},
		{
			name: "takeover larger than the target's net disbursement",
			body: map[string]any{"toLoanAccountId": targetID.String(), "transferDate": "2024-03-15", "mode": "takeover"},
			setup: func(mock pgxmock.PgxPoolIface) {
				sourceFixture(mock, 2, false)
				targetFixture(mock, "loan", false)
				// A 500.00 loan cannot settle a 1,009.05 balance.
				expectLoanScheduleRowsFor(mock, targetID, userID, money.FromFloat(500), 900, 24)
				expectNoLoanTransfers(mock, targetID, userID)
				expectLoanDisbursementCredit(mock, targetID, userID)
				expectLoanPayments(mock, targetID, userID)
			},
			want: "cannot cover the transferred balance",
			code: http.StatusBadRequest,
		},
		{
			name: "opens a target that already has a schedule",
			body: map[string]any{"toLoanAccountId": targetID.String(), "transferDate": "2024-03-15", "mode": "opens"},
			setup: func(mock pgxmock.PgxPoolIface) {
				sourceFixture(mock, 2, false)
				targetFixture(mock, "loan", false)
				expectLoanScheduleRowsFor(mock, targetID, userID, money.FromFloat(3000), 900, 24)
				expectNoLoanTransfers(mock, targetID, userID)
				expectLoanDisbursementCredit(mock, targetID, userID)
				expectLoanPayments(mock, targetID, userID)
			},
			want: "already has a schedule",
			code: http.StatusBadRequest,
		},
		{
			name: "unknown mode",
			body: map[string]any{"toLoanAccountId": targetID.String(), "transferDate": "2024-03-15", "mode": "absorb"},
			setup: func(mock pgxmock.PgxPoolIface) {
				sourceFixture(mock, 2, false)
				targetFixture(mock, "loan", false)
				expectLoanScheduleRowsFor(mock, targetID, userID, money.FromFloat(3000), 900, 24)
				expectNoLoanTransfers(mock, targetID, userID)
				expectLoanDisbursementCredit(mock, targetID, userID)
				expectLoanPayments(mock, targetID, userID)
			},
			want: "mode must be recast, opens or takeover",
			code: http.StatusBadRequest,
		},
		{
			name: "target has no schedule and no terms were supplied",
			body: map[string]any{"toLoanAccountId": targetID.String(), "transferDate": "2024-03-15"},
			setup: func(mock pgxmock.PgxPoolIface) {
				sourceFixture(mock, 2, false)
				targetFixture(mock, "loan", false)
				mock.ExpectQuery("FROM loan_schedules").WithArgs(targetID, userID).WillReturnError(pgx.ErrNoRows)
			},
			want: "provide targetAnnualRateBps",
			code: http.StatusBadRequest,
		},
		{
			name: "target terms are out of range",
			body: map[string]any{
				"toLoanAccountId": targetID.String(), "transferDate": "2024-03-15",
				"targetAnnualRateBps": -1, "targetTenureMonths": 12, "targetStartDate": "2024-07-01",
			},
			setup: func(mock pgxmock.PgxPoolIface) {
				sourceFixture(mock, 2, false)
				targetFixture(mock, "loan", false)
				mock.ExpectQuery("FROM loan_schedules").WithArgs(targetID, userID).WillReturnError(pgx.ErrNoRows)
			},
			want: "targetAnnualRateBps must not be negative",
			code: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock, err := pgxmock.NewPool()
			require.NoError(t, err)
			defer mock.Close()
			srv := newTestServer(mock)
			r := newLoanScheduleTestRouter(srv)

			tc.setup(mock)

			req := jsonRequest(t, http.MethodPost, "/accounts/"+sourceID.String()+"/loan-transfer", tc.body)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, tc.code, w.Code)
			assert.Contains(t, w.Body.String(), tc.want)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// A takeover is what a refinance does: the target keeps amortizing its own
// principal, and the balance it settles comes out of the cash it released.
func TestTransferLoanBalanceTakeoverLeavesTargetTableAlone(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	srv := newTestServer(mock)
	r := newLoanScheduleTestRouter(srv)

	userID := testUserID()
	sourceID, targetID := uuid.New(), uuid.New()
	paidTxn1, paidTxn2 := uuid.New(), uuid.New()
	transferDate := time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)

	// The source has paid its first two installments, leaving 1,009.05.
	mock.ExpectQuery("SELECT name, account_type_id FROM accounts").
		WithArgs(sourceID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"name", "account_type_id"}).AddRow("Car Loan", "loan"))
	expectLoanScheduleRows(mock, sourceID, userID, transferScheduleRow(uuid.New(), sourceID))
	expectNoLoanTransfers(mock, sourceID, userID)
	expectLoanDisbursementCredit(mock, sourceID, userID)
	expectLoanPayments(mock, sourceID, userID,
		[]any{paidTxn1, money.FromFloat(107), transferStart, "debit"},
		[]any{paidTxn2, money.FromFloat(107), transferSecondPayment, "debit"})

	// The target is a loan of its own: 3,000.00 at 9% over 24 months.
	mock.ExpectQuery("SELECT user_id, name, account_type_id, closed FROM accounts").
		WithArgs(targetID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "name", "account_type_id", "closed"}).
			AddRow(userID, "Home Loan", "loan", false))
	expectLoanScheduleRowsFor(mock, targetID, userID, money.FromFloat(3000), 900, 24)
	expectNoLoanTransfers(mock, targetID, userID)
	expectLoanDisbursementCredit(mock, targetID, userID)
	expectLoanPayments(mock, targetID, userID)

	// The transfer is recorded as a takeover, and nothing else is written.
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO loan_transfers").
		WithArgs(pgxmock.AnyArg(), userID, sourceID, targetID, money.FromFloat(1020.49), money.FromFloat(1009.05), transferDate, models.LoanTransferTakeover).
		WillReturnRows(pgxmock.NewRows([]string{"created_at"}).AddRow(transferDate))
	mock.ExpectCommit()

	expectLoanScheduleRows(mock, sourceID, userID, transferScheduleRow(uuid.New(), sourceID))
	mock.ExpectQuery("FROM loan_transfers").
		WithArgs(sourceID, userID).
		WillReturnRows(pgxmock.NewRows(loanTransferCols).
			AddRow(transferRowJSON(uuid.New(), sourceID, targetID, money.FromFloat(1020.49), money.FromFloat(1009.05), transferDate, models.LoanTransferTakeover)...))
	expectLoanDisbursementCredit(mock, sourceID, userID)
	expectLoanPayments(mock, sourceID, userID,
		[]any{paidTxn1, money.FromFloat(107), transferStart, "debit"},
		[]any{paidTxn2, money.FromFloat(107), transferSecondPayment, "debit"})
	expectLoanScheduleRowsFor(mock, targetID, userID, money.FromFloat(3000), 900, 24)
	mock.ExpectQuery("FROM loan_transfers").
		WithArgs(targetID, userID).
		WillReturnRows(pgxmock.NewRows(loanTransferCols).
			AddRow(transferRowJSON(uuid.New(), sourceID, targetID, money.FromFloat(1020.49), money.FromFloat(1009.05), transferDate, models.LoanTransferTakeover)...))
	expectLoanDisbursementCredit(mock, targetID, userID)
	expectLoanPayments(mock, targetID, userID)

	body := map[string]any{
		"toLoanAccountId": targetID.String(),
		"transferDate":    "2024-03-15",
		"mode":            "takeover",
	}
	req := jsonRequest(t, http.MethodPost, "/accounts/"+sourceID.String()+"/loan-transfer", body)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var res models.LoanTransferResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Equal(t, models.LoanTransferTakeover, res.Transfer.Mode)

	// The source is settled exactly as a recast transfer would settle it.
	require.NotNil(t, res.Source.SettledOn)
	assert.Zero(t, res.Source.OutstandingPrincipal)
	assert.True(t, res.Source.Completed)

	// The target's table is untouched: still 3,000.00 over 24 months at 138.00,
	// with nothing recast and its own debt unchanged.
	require.NotNil(t, res.Target.Schedule)
	assert.Equal(t, money.FromFloat(3000), res.Target.Schedule.Principal)
	assert.Equal(t, money.FromFloat(138), res.Target.EMI)
	assert.Equal(t, money.FromFloat(138), res.Target.Entries[0].Amount)
	assert.Equal(t, money.FromFloat(115.50), res.Target.Entries[0].Principal)
	assert.Equal(t, money.FromFloat(2884.50), res.Target.Entries[0].Balance)
	assert.False(t, res.Target.Entries[0].Recast)
	assert.Equal(t, money.FromFloat(3000), res.Target.OutstandingPrincipal)

	// But it released 1,009.05 less: that is the takeover.
	require.NotNil(t, res.Target.Disbursement)
	assert.Equal(t, money.FromFloat(3000), res.Target.Disbursement.Sanctioned)
	assert.Equal(t, money.FromFloat(1020.49), res.Target.Disbursement.PaidOut)
	assert.Equal(t, money.FromFloat(1979.51), res.Target.Disbursement.Net)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A malformed id or date never reaches the database.
func TestTransferLoanBalanceBadRequests(t *testing.T) {
	sourceID, targetID := uuid.New(), uuid.New()

	cases := []struct {
		name string
		path string
		body any
		want string
	}{
		{"invalid source id", "/accounts/nope/loan-transfer",
			map[string]any{"toLoanAccountId": targetID.String(), "transferDate": "2024-03-15"}, "invalid id"},
		{"missing target", "/accounts/" + sourceID.String() + "/loan-transfer",
			map[string]any{"transferDate": "2024-03-15"}, "toLoanAccountId"},
		{"bad transfer date", "/accounts/" + sourceID.String() + "/loan-transfer",
			map[string]any{"toLoanAccountId": targetID.String(), "transferDate": "15-03-2024"}, "invalid date"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newMockServer(t)
			r := newLoanScheduleTestRouter(srv)

			req := jsonRequest(t, http.MethodPost, tc.path, tc.body)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), tc.want)
		})
	}
}

// A losing race against another transfer of the same source is reported as a
// conflict rather than a server error, and nothing else is written.
func TestTransferLoanBalanceSettledConcurrently(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	srv := newTestServer(mock)
	r := newLoanScheduleTestRouter(srv)

	userID := testUserID()
	sourceID, targetID := uuid.New(), uuid.New()

	mock.ExpectQuery("SELECT name, account_type_id FROM accounts").
		WithArgs(sourceID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"name", "account_type_id"}).AddRow("Car Loan", "loan"))
	expectLoanScheduleRows(mock, sourceID, userID, transferScheduleRow(uuid.New(), sourceID))
	expectNoLoanTransfers(mock, sourceID, userID)
	expectLoanDisbursementCredit(mock, sourceID, userID)
	expectLoanPayments(mock, sourceID, userID)

	mock.ExpectQuery("SELECT user_id, name, account_type_id, closed FROM accounts").
		WithArgs(targetID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "name", "account_type_id", "closed"}).
			AddRow(userID, "Home Loan", "loan", false))
	expectLoanScheduleRows(mock, targetID, userID, transferScheduleRow(uuid.New(), targetID))
	expectNoLoanTransfers(mock, targetID, userID)
	expectLoanDisbursementCredit(mock, targetID, userID)
	expectLoanPayments(mock, targetID, userID)

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO loan_transfers").
		WithArgs(anyArgs(8)...).
		WillReturnError(&pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint"})
	mock.ExpectRollback()

	body := map[string]any{"toLoanAccountId": targetID.String(), "transferDate": "2024-03-15"}
	req := jsonRequest(t, http.MethodPost, "/accounts/"+sourceID.String()+"/loan-transfer", body)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "already settled")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteLoanTransfer(t *testing.T) {
	t.Run("removes the transfer and leaves an existing target schedule alone", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()
		srv := newTestServer(mock)
		r := newLoanScheduleTestRouter(srv)

		sourceID, targetID, transferID := uuid.New(), uuid.New(), uuid.New()
		amount := money.FromFloat(950)
		mock.ExpectBegin()
		mock.ExpectQuery("DELETE FROM loan_transfers").
			WithArgs(transferID, sourceID, testUserID()).
			WillReturnRows(pgxmock.NewRows([]string{"to_loan_account_id", "mode", "amount"}).AddRow(targetID, models.LoanTransferRecast, amount))
		mock.ExpectCommit()

		req, _ := http.NewRequest(http.MethodDelete,
			"/accounts/"+sourceID.String()+"/loan-transfer/"+transferID.String(), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		assert.JSONEq(t, `{"deleted":1}`, w.Body.String())
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("removes a schedule the transfer created", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()
		srv := newTestServer(mock)
		r := newLoanScheduleTestRouter(srv)

		sourceID, targetID, transferID := uuid.New(), uuid.New(), uuid.New()
		amount := money.FromFloat(950)
		mock.ExpectBegin()
		mock.ExpectQuery("DELETE FROM loan_transfers").
			WithArgs(transferID, sourceID, testUserID()).
			WillReturnRows(pgxmock.NewRows([]string{"to_loan_account_id", "mode", "amount"}).AddRow(targetID, models.LoanTransferOpens, amount))
		// The schedule is only removed while its principal is still the
		// transferred amount and no other transfer landed on the target.
		mock.ExpectQuery("DELETE FROM loan_schedules").
			WithArgs(targetID, testUserID(), amount).
			WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(uuid.New()))
		mock.ExpectCommit()

		req, _ := http.NewRequest(http.MethodDelete,
			"/accounts/"+sourceID.String()+"/loan-transfer/"+transferID.String(), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		assert.JSONEq(t, `{"deleted":1}`, w.Body.String())
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// The user edited the target's terms after the transfer opened its
	// schedule: those terms are theirs, so the transfer is not deleted at all
	// (a half-reverted pair of loans would be worse than a 409).
	t.Run("refuses to delete terms the user has since replaced", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()
		srv := newTestServer(mock)
		r := newLoanScheduleTestRouter(srv)

		sourceID, targetID, transferID := uuid.New(), uuid.New(), uuid.New()
		mock.ExpectBegin()
		mock.ExpectQuery("DELETE FROM loan_transfers").
			WithArgs(transferID, sourceID, testUserID()).
			WillReturnRows(pgxmock.NewRows([]string{"to_loan_account_id", "mode", "amount"}).
				AddRow(targetID, models.LoanTransferOpens, money.FromFloat(950)))
		// The guarded delete removes nothing — the target's principal is no
		// longer the transferred amount — and the schedule is still there.
		mock.ExpectQuery("DELETE FROM loan_schedules").
			WithArgs(targetID, testUserID(), money.FromFloat(950)).
			WillReturnError(pgx.ErrNoRows)
		mock.ExpectQuery("SELECT EXISTS").
			WithArgs(targetID, testUserID()).
			WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
		mock.ExpectRollback()

		req, _ := http.NewRequest(http.MethodDelete,
			"/accounts/"+sourceID.String()+"/loan-transfer/"+transferID.String(), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), "terms have changed")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// The user deleted the target's schedule outright: the transfer has nothing
	// left to undo there, so its removal is allowed (refusing it would leave the
	// source settled with no way back).
	t.Run("removes the transfer when the target's schedule is already gone", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()
		srv := newTestServer(mock)
		r := newLoanScheduleTestRouter(srv)

		sourceID, targetID, transferID := uuid.New(), uuid.New(), uuid.New()
		mock.ExpectBegin()
		mock.ExpectQuery("DELETE FROM loan_transfers").
			WithArgs(transferID, sourceID, testUserID()).
			WillReturnRows(pgxmock.NewRows([]string{"to_loan_account_id", "mode", "amount"}).
				AddRow(targetID, models.LoanTransferOpens, money.FromFloat(950)))
		mock.ExpectQuery("DELETE FROM loan_schedules").
			WithArgs(targetID, testUserID(), money.FromFloat(950)).
			WillReturnError(pgx.ErrNoRows)
		mock.ExpectQuery("SELECT EXISTS").
			WithArgs(targetID, testUserID()).
			WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
		mock.ExpectCommit()

		req, _ := http.NewRequest(http.MethodDelete,
			"/accounts/"+sourceID.String()+"/loan-transfer/"+transferID.String(), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"deleted":1}`, w.Body.String())
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("is idempotent", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()
		srv := newTestServer(mock)
		r := newLoanScheduleTestRouter(srv)

		sourceID, transferID := uuid.New(), uuid.New()
		mock.ExpectBegin()
		mock.ExpectQuery("DELETE FROM loan_transfers").
			WithArgs(transferID, sourceID, testUserID()).
			WillReturnError(pgx.ErrNoRows)
		mock.ExpectCommit()

		req, _ := http.NewRequest(http.MethodDelete,
			"/accounts/"+sourceID.String()+"/loan-transfer/"+transferID.String(), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		assert.JSONEq(t, `{"deleted":0}`, w.Body.String())
	})

	t.Run("rejects malformed ids", func(t *testing.T) {
		srv, _ := newMockServer(t)
		r := newLoanScheduleTestRouter(srv)

		for _, path := range []string{
			"/accounts/nope/loan-transfer/" + uuid.NewString(),
			"/accounts/" + uuid.NewString() + "/loan-transfer/nope",
		} {
			req, _ := http.NewRequest(http.MethodDelete, path, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)
		}
	})
}
