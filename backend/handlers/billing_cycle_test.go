package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v3"
	"github.com/stretchr/testify/assert"
)

func TestBillingCycleMonths(t *testing.T) {
	earliest := time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)

	t.Run("billing day already passed this month", func(t *testing.T) {
		// today = 2024-05-15, billing day = 5 -> the May 5 billing date has
		// passed, so the in-progress cycle ends in June. Cycles run from Dec
		// 2023 (one month before the earliest transaction) through June 2024.
		today := time.Date(2024, 5, 15, 0, 0, 0, 0, time.UTC)
		months := billingCycleMonths(earliest, today, 5)
		assert.Len(t, months, 7)
		assert.Equal(t, time.Date(2023, 12, 1, 0, 0, 0, 0, time.UTC), months[0])
		assert.Equal(t, time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC), months[6])
	})

	t.Run("billing day not reached yet", func(t *testing.T) {
		// today = 2024-05-03, billing day = 5 -> the May 5 billing date is still
		// in the future, so the in-progress cycle ends in May.
		today := time.Date(2024, 5, 3, 0, 0, 0, 0, time.UTC)
		months := billingCycleMonths(earliest, today, 5)
		assert.Len(t, months, 6)
		assert.Equal(t, time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC), months[5])
	})

	t.Run("billing day defaults to 1", func(t *testing.T) {
		today := time.Date(2024, 5, 15, 0, 0, 0, 0, time.UTC)
		months := billingCycleMonths(earliest, today, 0)
		// Billing day 1 has passed -> in-progress cycle ends in June.
		assert.Len(t, months, 7)
		assert.Equal(t, time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC), months[6])
	})
}

func TestCycleDates(t *testing.T) {
	// Cycle ending in Feb 2024 with billing day 5: Jan 6 – Feb 5.
	start, end := cycleDates(time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC), 5)
	assert.Equal(t, time.Date(2024, 1, 6, 0, 0, 0, 0, time.UTC), start)
	assert.Equal(t, time.Date(2024, 2, 5, 0, 0, 0, 0, time.UTC), end)

	// Billing day clamps to the month length (Feb 2024 has 29 days). The cycle
	// runs from the day after the previous billing date (Jan 31) through the
	// clamped billing date (Feb 29).
	start, end = cycleDates(time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC), 31)
	assert.Equal(t, time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC), start)
	assert.Equal(t, time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC), end)
}

func TestEnsureBillingCyclesUpToDate(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	userID := testUserID()
	acctID := uuid.New()

	// Alignment check: no stale cycles.
	mock.ExpectQuery("SELECT end_date FROM billing_cycles").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"end_date"}))

	// Earliest transaction.
	mock.ExpectQuery("SELECT MIN\\(date\\) FROM transactions").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"min"}).AddRow(time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)))

	// Latest existing cycle end date is in the future -> nothing to generate.
	mock.ExpectQuery("SELECT MAX\\(end_date\\) FROM billing_cycles").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"max"}).AddRow(dateOnly(time.Now()).AddDate(0, 1, 0)))

	// Back-fill unassigned transactions.
	mock.ExpectExec("UPDATE transactions t SET billing_cycle_id").
		WithArgs(acctID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	err = ensureBillingCycles(context.Background(), mock, userID, acctID, 1)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestEnsureBillingCyclesGenerates(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	userID := testUserID()
	acctID := uuid.New()

	// Earliest transaction is this month, so only a couple of cycles are needed.
	now := dateOnly(time.Now())
	earliest := time.Date(now.Year(), now.Month(), 10, 0, 0, 0, 0, time.UTC)

	// Alignment check: no existing cycles.
	mock.ExpectQuery("SELECT end_date FROM billing_cycles").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"end_date"}))

	mock.ExpectQuery("SELECT MIN\\(date\\) FROM transactions").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"min"}).AddRow(earliest))

	// No existing cycles.
	mock.ExpectQuery("SELECT MAX\\(end_date\\) FROM billing_cycles").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"max"}).AddRow(nil))

	// One INSERT per month from (earliest month - 1) through the in-progress
	// month. Cycles end on the account's billing day (the 1st by default).
	months := billingCycleMonths(earliest, now, 1)
	for _, ms := range months {
		start, end := cycleDates(ms, 1)
		mock.ExpectExec("INSERT INTO billing_cycles").
			WithArgs(acctID, userID, start, end, end.Format("Jan 2006")).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))
	}

	// Back-fill unassigned transactions.
	mock.ExpectExec("UPDATE transactions t SET billing_cycle_id").
		WithArgs(acctID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	err = ensureBillingCycles(context.Background(), mock, userID, acctID, 1)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestEnsureBillingCyclesRegeneratesOnBillingDayChange(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	userID := testUserID()
	acctID := uuid.New()

	now := dateOnly(time.Now())
	earliest := time.Date(now.Year(), now.Month(), 10, 0, 0, 0, 0, time.UTC)

	// Alignment check: an existing cycle ends on the 1st, but the account now
	// bills on the 5th -> the stale cycles are dropped and regenerated.
	mock.ExpectQuery("SELECT end_date FROM billing_cycles").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"end_date"}).AddRow(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)))

	// Detach transactions from the stale cycles, then drop the cycles.
	mock.ExpectExec("UPDATE transactions SET billing_cycle_id = NULL").
		WithArgs(userID, acctID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectExec("DELETE FROM billing_cycles WHERE account_id").
		WithArgs(acctID, userID).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	// Earliest transaction.
	mock.ExpectQuery("SELECT MIN\\(date\\) FROM transactions").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"min"}).AddRow(earliest))

	// No cycles remain -> regenerate on the new billing day.
	mock.ExpectQuery("SELECT MAX\\(end_date\\) FROM billing_cycles").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"max"}).AddRow(nil))

	months := billingCycleMonths(earliest, now, 5)
	for _, ms := range months {
		start, end := cycleDates(ms, 5)
		mock.ExpectExec("INSERT INTO billing_cycles").
			WithArgs(acctID, userID, start, end, end.Format("Jan 2006")).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))
	}

	// Back-fill unassigned transactions.
	mock.ExpectExec("UPDATE transactions t SET billing_cycle_id").
		WithArgs(acctID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	err = ensureBillingCycles(context.Background(), mock, userID, acctID, 5)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetBillingCycles(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.GET("/accounts/:id/billing-cycles", GetBillingCycles)

	userID := testUserID()
	acctID := uuid.New()
	cycleID := uuid.New()

	// Account lookup (credit card with a billing day).
	mock.ExpectQuery("SELECT a.billing_day").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"billing_day"}).AddRow(intPtr(5)))

	// ensureBillingCycles: alignment check (no stale cycles).
	mock.ExpectQuery("SELECT end_date FROM billing_cycles").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"end_date"}))

	// ensureBillingCycles: earliest transaction.
	mock.ExpectQuery("SELECT MIN\\(date\\) FROM transactions").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"min"}).AddRow(time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)))

	// ensureBillingCycles: latest cycle end in the future -> nothing to generate.
	mock.ExpectQuery("SELECT MAX\\(end_date\\) FROM billing_cycles").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"max"}).AddRow(dateOnly(time.Now()).AddDate(0, 1, 0)))

	// ensureBillingCycles: back-fill.
	mock.ExpectExec("UPDATE transactions t SET billing_cycle_id").
		WithArgs(acctID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	// listBillingCycles.
	mock.ExpectQuery("SELECT bc.id, bc.start_date, bc.end_date, bc.label").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "start_date", "end_date", "label", "net_activity", "txn_count"}).
			AddRow(cycleID, time.Date(2024, 1, 6, 0, 0, 0, 0, time.UTC), time.Date(2024, 2, 5, 0, 0, 0, 0, time.UTC), "Feb 2024", 150.0, 2))

	req, _ := http.NewRequest("GET", "/accounts/"+acctID.String()+"/billing-cycles", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var res struct {
		Data []models.BillingCycle `json:"data"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Len(t, res.Data, 1)
	assert.Equal(t, cycleID, res.Data[0].ID)
	assert.Equal(t, 150.0, res.Data[0].TotalOutstanding)
	assert.Equal(t, 2, res.Data[0].TransactionCount)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetBillingCyclesNetOutstanding(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.GET("/accounts/:id/billing-cycles", GetBillingCycles)

	userID := testUserID()
	acctID := uuid.New()
	cycleA := uuid.New()
	cycleB := uuid.New()

	// Account lookup (credit card with a billing day).
	mock.ExpectQuery("SELECT a.billing_day").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"billing_day"}).AddRow(intPtr(5)))

	// ensureBillingCycles: alignment check (no stale cycles).
	mock.ExpectQuery("SELECT end_date FROM billing_cycles").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"end_date"}))

	// ensureBillingCycles: earliest transaction.
	mock.ExpectQuery("SELECT MIN\\(date\\) FROM transactions").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"min"}).AddRow(time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)))

	// ensureBillingCycles: latest cycle end in the future -> nothing to generate.
	mock.ExpectQuery("SELECT MAX\\(end_date\\) FROM billing_cycles").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"max"}).AddRow(dateOnly(time.Now()).AddDate(0, 1, 0)))

	// ensureBillingCycles: back-fill.
	mock.ExpectExec("UPDATE transactions t SET billing_cycle_id").
		WithArgs(acctID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	// listBillingCycles: net activity per cycle — cycle A: 10000 purchases;
	// cycle B: 3000 purchases − 500 refund − 8000 bill payment = −5500.
	mock.ExpectQuery("SELECT bc.id, bc.start_date, bc.end_date, bc.label").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "start_date", "end_date", "label", "net_activity", "txn_count"}).
			AddRow(cycleA, time.Date(2024, 1, 6, 0, 0, 0, 0, time.UTC), time.Date(2024, 2, 5, 0, 0, 0, 0, time.UTC), "Feb 2024", 10000.0, 2).
			AddRow(cycleB, time.Date(2024, 2, 6, 0, 0, 0, 0, time.UTC), time.Date(2024, 3, 5, 0, 0, 0, 0, time.UTC), "Mar 2024", -5500.0, 3))

	req, _ := http.NewRequest("GET", "/accounts/"+acctID.String()+"/billing-cycles", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var res struct {
		Data []models.BillingCycle `json:"data"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Len(t, res.Data, 2)
	// Running balance through each cycle end: A = 10000, B = 10000 − 5500 = 4500.
	assert.Equal(t, cycleA, res.Data[0].ID)
	assert.Equal(t, 10000.0, res.Data[0].TotalOutstanding)
	assert.Equal(t, cycleB, res.Data[1].ID)
	assert.Equal(t, 4500.0, res.Data[1].TotalOutstanding)
	assert.Equal(t, 3, res.Data[1].TransactionCount)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetBillingCyclesNoBillingDay(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.GET("/accounts/:id/billing-cycles", GetBillingCycles)

	userID := testUserID()
	acctID := uuid.New()

	// Account lookup (no billing day -> empty list, no cycle logic).
	mock.ExpectQuery("SELECT a.billing_day").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"billing_day"}).AddRow(nil))

	req, _ := http.NewRequest("GET", "/accounts/"+acctID.String()+"/billing-cycles", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"data":[]}`, w.Body.String())
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetBillingCyclesAccountNotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.GET("/accounts/:id/billing-cycles", GetBillingCycles)

	userID := testUserID()
	acctID := uuid.New()

	mock.ExpectQuery("SELECT a.billing_day").
		WithArgs(acctID, userID).
		WillReturnError(pgx.ErrNoRows)

	req, _ := http.NewRequest("GET", "/accounts/"+acctID.String()+"/billing-cycles", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}
