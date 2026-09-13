package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/fintrak/backend/internal/money"
	"github.com/fintrak/backend/models"
	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTransactionDates(t *testing.T) {
	txns := []models.ImportTransaction{
		{Date: "2024-03-05"},
		{Date: "not-a-date"},
		{Date: "2024-01-10"},
		{Date: "2024-03-05"}, // duplicate
	}

	dates := transactionDates(txns)

	require.Len(t, dates, 2)
	assert.Equal(t, time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC), dates[0])
	assert.Equal(t, time.Date(2024, 3, 5, 0, 0, 0, 0, time.UTC), dates[1])
}

func TestLoadExistingFingerprintsErrors(t *testing.T) {
	userID := testUserID()
	acctID := uuid.New()
	txns := []models.ImportTransaction{{Date: "2024-01-10"}}

	t.Run("no valid dates skips the query", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		got, err := loadExistingFingerprints(context.Background(), mock, acctID, userID,
			[]models.ImportTransaction{{Date: "bad"}})
		require.NoError(t, err)
		assert.Empty(t, got)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectQuery("SELECT date, amount, type, description FROM transactions").
			WithArgs(acctID, userID, pgxmock.AnyArg()).
			WillReturnError(assert.AnError)

		_, err = loadExistingFingerprints(context.Background(), mock, acctID, userID, txns)
		assert.Error(t, err)
	})

	t.Run("scan error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectQuery("SELECT date, amount, type, description FROM transactions").
			WithArgs(acctID, userID, pgxmock.AnyArg()).
			WillReturnRows(pgxmock.NewRows([]string{"date"}).AddRow(time.Now()))

		_, err = loadExistingFingerprints(context.Background(), mock, acctID, userID, txns)
		assert.Error(t, err)
	})

	t.Run("rows error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		rows := pgxmock.NewRows([]string{"date", "amount", "type", "description"}).
			AddRow(time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC), money.FromFloat(1), "debit", "Coffee").
			RowError(0, assert.AnError)
		mock.ExpectQuery("SELECT date, amount, type, description FROM transactions").
			WithArgs(acctID, userID, pgxmock.AnyArg()).
			WillReturnRows(rows)

		_, err = loadExistingFingerprints(context.Background(), mock, acctID, userID, txns)
		assert.Error(t, err)
	})

	t.Run("success builds fingerprints", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectQuery("SELECT date, amount, type, description FROM transactions").
			WithArgs(acctID, userID, pgxmock.AnyArg()).
			WillReturnRows(pgxmock.NewRows([]string{"date", "amount", "type", "description"}).
				AddRow(time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC), money.FromFloat(1), "debit", "Coffee"))

		got, err := loadExistingFingerprints(context.Background(), mock, acctID, userID, txns)
		require.NoError(t, err)
		assert.Len(t, got, 1)
		assert.True(t, got[transactionFingerprint("2024-01-10", money.FromFloat(1), "debit", "Coffee")])
	})
}
