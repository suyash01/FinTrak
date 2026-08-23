package handlers

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v3"
	"github.com/stretchr/testify/assert"
)

func TestComputeCreditCardSummaryRows(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	userID := testUserID()
	acctID := uuid.New()

	cycleID := func(n int) uuid.UUID {
		return uuid.NewSHA1(uuid.NameSpaceOID, fmt.Appendf(nil, "cycle-%d", n))
	}

	// listBillingCycles: Jan 6–Feb 5 (150, 2), Feb 6–Mar 5 (200, 2),
	// Mar 6–Apr 5 (60, 1), Apr 6–May 5 (0, 0), May 6–Jun 5 (0, 0).
	mock.ExpectQuery("SELECT bc.id, bc.start_date, bc.end_date, bc.label").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "start_date", "end_date", "label", "total_outstanding", "txn_count"}).
			AddRow(cycleID(1), time.Date(2024, 1, 6, 0, 0, 0, 0, time.UTC), time.Date(2024, 2, 5, 0, 0, 0, 0, time.UTC), "Feb 2024", 150.0, 2).
			AddRow(cycleID(2), time.Date(2024, 2, 6, 0, 0, 0, 0, time.UTC), time.Date(2024, 3, 5, 0, 0, 0, 0, time.UTC), "Mar 2024", 200.0, 2).
			AddRow(cycleID(3), time.Date(2024, 3, 6, 0, 0, 0, 0, time.UTC), time.Date(2024, 4, 5, 0, 0, 0, 0, time.UTC), "Apr 2024", 60.0, 1).
			AddRow(cycleID(4), time.Date(2024, 4, 6, 0, 0, 0, 0, time.UTC), time.Date(2024, 5, 5, 0, 0, 0, 0, time.UTC), "May 2024", 0.0, 0).
			AddRow(cycleID(5), time.Date(2024, 5, 6, 0, 0, 0, 0, time.UTC), time.Date(2024, 6, 5, 0, 0, 0, 0, time.UTC), "Jun 2024", 0.0, 0))

	// Current in-progress cycle (Mar 6–Apr 5 contains Mar 31): debits up to Mar 31.
	mock.ExpectQuery("SELECT COALESCE\\(SUM\\(CASE WHEN t.type = 'debit'").
		WithArgs(cycleID(3), time.Date(2024, 3, 31, 0, 0, 0, 0, time.UTC)).
		WillReturnRows(pgxmock.NewRows([]string{"total", "count"}).AddRow(60.0, 1))

	rows := computeCreditCardSummaryRows(c, userID, acctID, "Amex", "2024-01-01", "2024-03-31")

	// Feb 5 (Jan debits = 150), Mar 5 (Feb debits = 200), and a current-cycle
	// row at Mar 31 (Mar debits = 60).
	assert.Len(t, rows, 3)
	assert.Equal(t, "Total outstanding", rows[0].Description)
	assert.Equal(t, time.Date(2024, 2, 5, 0, 0, 0, 0, time.UTC), dateOnly(rows[0].Date))
	assert.Equal(t, 150.0, rows[0].Amount)
	assert.Equal(t, time.Date(2024, 3, 5, 0, 0, 0, 0, time.UTC), dateOnly(rows[1].Date))
	assert.Equal(t, 200.0, rows[1].Amount)
	assert.Equal(t, time.Date(2024, 3, 31, 0, 0, 0, 0, time.UTC), dateOnly(rows[2].Date))
	assert.Equal(t, 60.0, rows[2].Amount)
	assert.True(t, rows[0].IsSummary)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestComputeCreditCardSummaryRowsFirstOfMonth(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	userID := testUserID()
	acctID := uuid.New()

	cycleID := func(n int) uuid.UUID {
		return uuid.NewSHA1(uuid.NameSpaceOID, fmt.Appendf(nil, "cycle-%d", n))
	}

	// Billing day defaults to the 1st: Dec 2–Jan 1 (0, 0), Jan 2–Feb 1 (100, 1),
	// Feb 2–Mar 1 (0, 0), Mar 2–Apr 1 (0, 0).
	mock.ExpectQuery("SELECT bc.id, bc.start_date, bc.end_date, bc.label").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "start_date", "end_date", "label", "total_outstanding", "txn_count"}).
			AddRow(cycleID(1), time.Date(2023, 12, 2, 0, 0, 0, 0, time.UTC), time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), "Jan 2024", 0.0, 0).
			AddRow(cycleID(2), time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC), "Feb 2024", 100.0, 1).
			AddRow(cycleID(3), time.Date(2024, 2, 2, 0, 0, 0, 0, time.UTC), time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC), "Mar 2024", 0.0, 0).
			AddRow(cycleID(4), time.Date(2024, 3, 2, 0, 0, 0, 0, time.UTC), time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC), "Apr 2024", 0.0, 0))

	// Current in-progress cycle (Mar 2–Apr 1 contains Mar 31): no debits yet.
	mock.ExpectQuery("SELECT COALESCE\\(SUM\\(CASE WHEN t.type = 'debit'").
		WithArgs(cycleID(4), time.Date(2024, 3, 31, 0, 0, 0, 0, time.UTC)).
		WillReturnRows(pgxmock.NewRows([]string{"total", "count"}).AddRow(0.0, 0))

	rows := computeCreditCardSummaryRows(c, userID, acctID, "Amex", "2024-01-01", "2024-03-31")

	var found float64
	for _, r := range rows {
		if r.Description == "Total outstanding" && dateOnly(r.Date).Equal(time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)) {
			found = r.Amount
		}
	}
	assert.Equal(t, 100.0, found)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestComputeSummaryRowsBank(t *testing.T) {
	acctID := uuid.New().String()
	// Leap year 2024: Feb has 29 days.
	txns := []acctTxn{
		{Date: time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC), Amount: 1000, Type: "credit"},
		{Date: time.Date(2024, 1, 20, 0, 0, 0, 0, time.UTC), Amount: 300, Type: "debit"},
		{Date: time.Date(2024, 2, 5, 0, 0, 0, 0, time.UTC), Amount: 500, Type: "credit"},
	}

	rows := computeSummaryRows("Savings", acctID, "bank", "credit", "2024-01-01", "2024-02-29", txns)

	assert.Len(t, rows, 2)
	assert.Equal(t, time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC), dateOnly(rows[0].Date))
	assert.Equal(t, 700.0, rows[0].Amount)
	assert.Equal(t, "Month-end balance", rows[0].Description)
	assert.Equal(t, time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC), dateOnly(rows[1].Date))
	assert.Equal(t, 1200.0, rows[1].Amount)
}

func TestComputeSummaryRowsBankDebitPositive(t *testing.T) {
	acctID := uuid.New().String()
	txns := []acctTxn{
		{Date: time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC), Amount: 1000, Type: "debit"},
		{Date: time.Date(2024, 1, 20, 0, 0, 0, 0, time.UTC), Amount: 300, Type: "credit"},
	}

	// For a debit-positive account, debits add to the balance.
	rows := computeSummaryRows("Loan", acctID, "bank", "debit", "2024-01-01", "2024-01-31", txns)
	assert.Len(t, rows, 1)
	assert.Equal(t, 700.0, rows[0].Amount)
}

func TestComputeSummaryRowsBankMidMonthRange(t *testing.T) {
	acctID := uuid.New().String()
	txns := []acctTxn{
		{Date: time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC), Amount: 1000, Type: "credit"},
		{Date: time.Date(2024, 1, 20, 0, 0, 0, 0, time.UTC), Amount: 300, Type: "debit"},
		{Date: time.Date(2024, 2, 5, 0, 0, 0, 0, time.UTC), Amount: 500, Type: "credit"},
		{Date: time.Date(2024, 2, 10, 0, 0, 0, 0, time.UTC), Amount: 100, Type: "debit"},
	}

	// Range ends mid-month: a "Balance" row is added at the range end.
	rows := computeSummaryRows("Savings", acctID, "bank", "credit", "2024-01-01", "2024-02-15", txns)

	assert.Len(t, rows, 2)
	assert.Equal(t, "Month-end balance", rows[0].Description)
	assert.Equal(t, time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC), dateOnly(rows[0].Date))
	assert.Equal(t, 700.0, rows[0].Amount)
	assert.Equal(t, "Balance", rows[1].Description)
	assert.Equal(t, time.Date(2024, 2, 15, 0, 0, 0, 0, time.UTC), dateOnly(rows[1].Date))
	assert.Equal(t, 1100.0, rows[1].Amount)
}

func TestMergeSummaryRows(t *testing.T) {
	mkTxn := func(day int) models.Transaction {
		return models.Transaction{ID: uuid.New(), Date: time.Date(2024, 1, day, 0, 0, 0, 0, time.UTC)}
	}
	// transactions sorted DESC by date: 10, 8, 5
	txns := []models.Transaction{mkTxn(10), mkTxn(8), mkTxn(5)}
	// summary rows: a row on day 9 and day 6
	summary := []models.Transaction{mkTxn(9), mkTxn(6)}

	merged := mergeSummaryRows(txns, summary, "date", "DESC")
	got := make([]int, len(merged))
	for i, m := range merged {
		got[i] = m.Date.Day()
	}
	assert.Equal(t, []int{10, 9, 8, 6, 5}, got)
}
