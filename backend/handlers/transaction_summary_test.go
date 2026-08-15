package handlers

import (
	"testing"
	"time"

	"github.com/fintrak/backend/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestLastBillingDate(t *testing.T) {
	t.Run("billing day already passed this month", func(t *testing.T) {
		// today = 2024-05-15, billing day = 5 -> last billing date is 2024-05-05.
		today := time.Date(2024, 5, 15, 0, 0, 0, 0, time.UTC)
		assert.Equal(t, time.Date(2024, 5, 5, 0, 0, 0, 0, time.UTC), lastBillingDate(today, 5))
	})
	t.Run("billing day is today", func(t *testing.T) {
		today := time.Date(2024, 5, 5, 0, 0, 0, 0, time.UTC)
		assert.Equal(t, today, lastBillingDate(today, 5))
	})
	t.Run("billing day not reached yet falls to previous month", func(t *testing.T) {
		// today = 2024-05-03, billing day = 5 -> last billing date is 2024-04-05.
		today := time.Date(2024, 5, 3, 0, 0, 0, 0, time.UTC)
		assert.Equal(t, time.Date(2024, 4, 5, 0, 0, 0, 0, time.UTC), lastBillingDate(today, 5))
	})
	t.Run("billing day clamps to month length", func(t *testing.T) {
		// today = 2024-02-28, billing day = 31. Feb's billing date (29th) is still
		// in the future, so the last billing date falls back to Jan 31.
		today := time.Date(2024, 2, 28, 0, 0, 0, 0, time.UTC)
		assert.Equal(t, time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC), lastBillingDate(today, 31))
	})
}

func TestComputeSummaryRowsCreditCard(t *testing.T) {
	acctID := uuid.New().String()
	txns := []acctTxn{
		{Date: time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC), Amount: 100, Type: "debit"},
		{Date: time.Date(2024, 1, 20, 0, 0, 0, 0, time.UTC), Amount: 50, Type: "debit"},
		{Date: time.Date(2024, 2, 10, 0, 0, 0, 0, time.UTC), Amount: 200, Type: "debit"},
		{Date: time.Date(2024, 2, 20, 0, 0, 0, 0, time.UTC), Amount: 30, Type: "credit"},
		{Date: time.Date(2024, 3, 10, 0, 0, 0, 0, time.UTC), Amount: 60, Type: "debit"},
	}

	// Billing day 5 -> rows at each billing date that has transactions plus a
	// final current-cycle row. The Jan 5 cycle has no purchases, so it is
	// omitted.
	rows := computeSummaryRows("Amex", acctID, "credit_card", "credit", 5, "2024-01-01", "2024-03-31", txns)

	// Feb 5 (Jan debits = 150), Mar 5 (Feb debits = 200), and a current-cycle
	// row at Mar 31 (Mar debits = 60).
	assert.Len(t, rows, 3)
	assert.Equal(t, "Total outstanding", rows[0].Description)
	assert.Equal(t, time.Date(2024, 2, 5, 0, 0, 0, 0, time.UTC), dateOnly(rows[0].Date))
	assert.Equal(t, 150.0, rows[0].Amount)
	assert.Equal(t, time.Date(2024, 3, 5, 0, 0, 0, 0, time.UTC), dateOnly(rows[1].Date))
	// Only debit (purchase) transactions count; the credit is not netted out.
	assert.Equal(t, 200.0, rows[1].Amount)
	assert.Equal(t, time.Date(2024, 3, 31, 0, 0, 0, 0, time.UTC), dateOnly(rows[2].Date))
	assert.Equal(t, 60.0, rows[2].Amount)
	assert.True(t, rows[0].IsSummary)
}

func TestComputeSummaryRowsCreditCardDefaultsBillingDay(t *testing.T) {
	acctID := uuid.New().String()
	txns := []acctTxn{
		{Date: time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC), Amount: 100, Type: "debit"},
	}

	// No billing day set -> defaults to the 1st; a row appears at each month's
	// 1st, and the debit lands on the Feb 1 row.
	rows := computeSummaryRows("Amex", acctID, "credit_card", "credit", 0, "2024-01-01", "2024-03-31", txns)

	var found float64
	for _, r := range rows {
		if r.Description == "Total outstanding" && dateOnly(r.Date).Equal(time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)) {
			found = r.Amount
		}
	}
	assert.Equal(t, 100.0, found)
}

func TestComputeSummaryRowsBank(t *testing.T) {
	acctID := uuid.New().String()
	// Leap year 2024: Feb has 29 days.
	txns := []acctTxn{
		{Date: time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC), Amount: 1000, Type: "credit"},
		{Date: time.Date(2024, 1, 20, 0, 0, 0, 0, time.UTC), Amount: 300, Type: "debit"},
		{Date: time.Date(2024, 2, 5, 0, 0, 0, 0, time.UTC), Amount: 500, Type: "credit"},
	}

	rows := computeSummaryRows("Savings", acctID, "bank", "credit", 0, "2024-01-01", "2024-02-29", txns)

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
	rows := computeSummaryRows("Loan", acctID, "bank", "debit", 0, "2024-01-01", "2024-01-31", txns)
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
	rows := computeSummaryRows("Savings", acctID, "bank", "credit", 0, "2024-01-01", "2024-02-15", txns)

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