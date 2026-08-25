package db

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v3"
	"github.com/stretchr/testify/assert"
)

func TestSeedDefaultCategoriesInsertsWhenEmpty(t *testing.T) {
	mock := setupMock(t)
	ctx := context.Background()
	userID := uuid.New()

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM categories WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))

	seedArgs := make([]interface{}, 25*6)
	for i := range seedArgs {
		seedArgs[i] = pgxmock.AnyArg()
	}
	mock.ExpectExec("INSERT INTO categories").
		WithArgs(seedArgs...).
		WillReturnResult(pgxmock.NewResult("INSERT", 25))

	SeedDefaultCategories(ctx, userID)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSeedDefaultCategoriesSkipsWhenAlreadySeeded(t *testing.T) {
	mock := setupMock(t)
	ctx := context.Background()
	userID := uuid.New()

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM categories WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))

	SeedDefaultCategories(ctx, userID)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSeedDefaultCategoriesLogsOnCountError(t *testing.T) {
	mock := setupMock(t)
	ctx := context.Background()
	userID := uuid.New()

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM categories WHERE user_id").
		WithArgs(userID).
		WillReturnError(assert.AnError)

	SeedDefaultCategories(ctx, userID)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSeedAccountTypes(t *testing.T) {
	mock := setupMock(t)

	mock.ExpectExec("INSERT INTO account_types").
		WithArgs("bank", "Bank Account", "credit").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO account_types").
		WithArgs("credit_card", "Credit Card", "credit").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	SeedAccountTypes()

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPromoteAdminUsers(t *testing.T) {
	mock := setupMock(t)

	mock.ExpectExec("UPDATE users SET role = 'admin'").
		WithArgs("admin@example.com").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("UPDATE users SET role = 'admin'").
		WithArgs("owner@example.com").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	PromoteAdminUsers([]string{"admin@example.com", "owner@example.com"})

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPromoteAdminUsersEmptyListIsNoop(t *testing.T) {
	mock := setupMock(t)

	PromoteAdminUsers(nil)
	PromoteAdminUsers([]string{"", "  "})

	assert.NoError(t, mock.ExpectationsWereMet())
}
