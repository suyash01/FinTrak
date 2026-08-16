package db

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupMock(t *testing.T) pgxmock.PgxPoolIface {
	t.Helper()
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	t.Cleanup(mock.Close)

	oldPool := Pool
	Pool = mock
	t.Cleanup(func() { Pool = oldPool })

	return mock
}

func TestWithTxCommitsOnSuccess(t *testing.T) {
	mock := setupMock(t)
	ctx := context.Background()

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE transactions SET notes").
		WithArgs("updated", int64(1)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	err := WithTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "UPDATE transactions SET notes = $1 WHERE id = $2", "updated", int64(1))
		return err
	})

	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestWithTxRollsBackOnError(t *testing.T) {
	mock := setupMock(t)
	ctx := context.Background()

	sentinel := errors.New("boom")
	mock.ExpectBegin()
	mock.ExpectRollback()

	err := WithTx(ctx, func(tx pgx.Tx) error {
		return sentinel
	})

	assert.ErrorIs(t, err, sentinel)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestWithTxReturnsBeginError(t *testing.T) {
	mock := setupMock(t)
	ctx := context.Background()

	beginErr := errors.New("cannot begin transaction")
	mock.ExpectBegin().WillReturnError(beginErr)

	err := WithTx(ctx, func(tx pgx.Tx) error {
		t.Fatal("transaction function must not be called when Begin fails")
		return nil
	})

	assert.ErrorIs(t, err, beginErr)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestWithTxReturnsCommitError(t *testing.T) {
	mock := setupMock(t)
	ctx := context.Background()

	commitErr := errors.New("commit failed")
	mock.ExpectBegin()
	mock.ExpectCommit().WillReturnError(commitErr)

	err := WithTx(ctx, func(tx pgx.Tx) error {
		return nil
	})

	assert.ErrorIs(t, err, commitErr)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCloseWithNilPool(t *testing.T) {
	oldPool := Pool
	Pool = nil
	defer func() { Pool = oldPool }()

	// Close must be a no-op (not panic) when the pool is nil.
	assert.NotPanics(t, Close)
}
