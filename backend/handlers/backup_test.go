package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
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

func newBackupTestRouter(t *testing.T) (*gin.Engine, *Server, pgxmock.PgxPoolIface) {
	t.Helper()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(mock)
	t.Cleanup(mock.Close)

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.GET("/export", srv.ExportUserData)
	r.POST("/import", srv.ImportUserData)
	return r, srv, mock
}

func postBackup(t *testing.T, r *gin.Engine, bundle any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(bundle)
	require.NoError(t, err)
	req, _ := http.NewRequest("POST", "/import", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestExportUserData(t *testing.T) {
	r, _, mock := newBackupTestRouter(t)

	userID := testUserID()
	now := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	accountID := uuid.New()
	loanID := uuid.New()
	categoryID := uuid.New()
	payeeID := uuid.New()
	txnID := uuid.New()
	seriesID := uuid.New()

	mock.ExpectQuery("FROM users WHERE id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"paperless_url", "paperless_tag", "page_size"}).
			AddRow("http://paperless", "fintrak", intPtr(25)))

	mock.ExpectQuery("FROM accounts WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "account_type_id", "bank", "currency", "color", "is_default", "billing_day", "closed", "created_at", "updated_at"}).
			AddRow(accountID, "Savings", "bank", "HDFC", "INR", "#000000", true, intPtr(5), false, now, now))

	mock.ExpectQuery("FROM category_groups WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "icon", "color", "sort_order"}).
			AddRow("custom", "Custom", "star", "#111111", 1))

	mock.ExpectQuery("FROM categories WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "icon", "color", "group_id"}).
			AddRow(categoryID, "Food", "utensils", "#f97316", "expense"))

	// The global categories the user references must include the ones a rule
	// filter points at (filter_category_id), not just the ones a rule,
	// transaction or series categorizes with: the restore resolves the filter
	// through the same map, so a bundle without it restores that rule with the
	// filter cleared.
	mock.ExpectQuery("c.id IN \\(SELECT filter_category_id FROM rules WHERE user_id = \\$1 AND filter_category_id IS NOT NULL\\)").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "icon", "color", "group_id"}).
			AddRow(uuid.New(), "GlobalCat", "", "", "expense"))

	mock.ExpectQuery("FROM payees WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "account_id", "created_at", "updated_at"}).
			AddRow(payeeID, "Merchant", &accountID, now, now))

	mock.ExpectQuery("FROM billing_cycles WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "account_id", "start_date", "end_date", "label", "created_at"}).
			AddRow(uuid.New(), accountID, now, now, "Jan", now))

	mock.ExpectQuery("FROM transactions WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "account_id", "date", "description", "amount", "type", "category_id", "tags", "notes", "payee_id", "billing_cycle_id", "created_at", "updated_at"}).
			AddRow(txnID, accountID, now, "Coffee", 250.5, "debit", &categoryID, []string{"food"}, "morning", &payeeID, nil, now, now))

	mock.ExpectQuery("FROM links WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "type", "from_txn_id", "to_txn_id", "notes", "created_at"}).
			AddRow(uuid.New(), "transfer", txnID, uuid.New(), "note", now))

	mock.ExpectQuery("FROM loan_attachments WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "loan_account_id", "transaction_id", "created_at"}).
			AddRow(uuid.New(), loanID, txnID, now))

	mock.ExpectQuery("FROM loan_schedules WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "loan_account_id", "principal", "processing_fee", "annual_rate_bps", "tenure_months", "start_date", "disbursal_date", "created_at", "updated_at"}).
			AddRow(uuid.New(), loanID, int64(100000), int64(2500), 900, 24, now, nil, now, now))

	mock.ExpectQuery("FROM loan_transfers WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "from_loan_account_id", "to_loan_account_id", "amount", "principal", "transfer_date", "mode", "created_at"}).
			AddRow(uuid.New(), loanID, uuid.New(), int64(40000), int64(39000), now, "recast", now))

	mock.ExpectQuery("FROM loan_disbursements WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "loan_account_id", "transaction_id", "created_at"}).
			AddRow(uuid.New(), loanID, txnID, now))

	mock.ExpectQuery("FROM recurring_series WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "description", "type", "frequency", "interval", "category_id", "payee_id", "active", "notes", "created_at", "updated_at"}).
			AddRow(seriesID, "Rent", "desc", "debit", "monthly", 1, &categoryID, &payeeID, true, "note", now, now))

	mock.ExpectQuery("FROM recurring_series_terms WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "series_id", "start_date", "end_date", "amount", "account_id", "created_at"}).
			AddRow(uuid.New(), seriesID, now, &now, 1000.0, accountID, now))

	mock.ExpectQuery("FROM recurring_attachments WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "series_id", "transaction_id", "created_at"}).
			AddRow(uuid.New(), seriesID, txnID, now))

	mock.ExpectQuery("FROM rules WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "pattern", "match_type", "category_id", "payee_id", "priority",
			"account_id", "filter_category_id", "filter_payee_id", "min_amount", "max_amount", "txn_type",
			"date_from", "date_to", "is_linked", "is_recurring", "add_tags", "notes"}).
			AddRow(uuid.New(), "coffee", "contains", categoryID, &payeeID, 1,
				nil, nil, nil, nil, nil, "", nil, nil, nil, nil, []string{}, ""))

	req, _ := http.NewRequest("GET", "/export", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Disposition"), "fintrak-backup")
	assert.Contains(t, w.Header().Get("Content-Disposition"), ".json")

	var bundle models.BackupBundle
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &bundle))
	assert.Equal(t, models.BackupFormat, bundle.Format)
	assert.Equal(t, models.BackupVersion, bundle.Version)
	require.NotNil(t, bundle.Settings)
	assert.Equal(t, "http://paperless", bundle.Settings.PaperlessURL)
	require.Len(t, bundle.Accounts, 1)
	assert.Equal(t, "Savings", bundle.Accounts[0].Name)
	require.Len(t, bundle.Categories, 2)
	require.Len(t, bundle.Transactions, 1)
	assert.Equal(t, "2024-01-15", bundle.Transactions[0].Date)
	require.Len(t, bundle.Links, 1)
	require.Len(t, bundle.LoanSchedules, 1)
	assert.Equal(t, int64(100000), int64(bundle.LoanSchedules[0].Principal))
	assert.Equal(t, int64(2500), int64(bundle.LoanSchedules[0].ProcessingFee))
	assert.Equal(t, 900, bundle.LoanSchedules[0].AnnualRateBps)
	assert.Empty(t, bundle.LoanSchedules[0].DisbursalDate)
	// Both loans' amortization tables are derived from the transfer rows, so
	// they travel with the bundle.
	require.Len(t, bundle.LoanTransfers, 1)
	assert.Equal(t, int64(40000), int64(bundle.LoanTransfers[0].Amount))
	assert.Equal(t, "recast", bundle.LoanTransfers[0].Mode)
	// The payoff's split travels with it, so a restore keeps the interest apart
	// from the principal.
	assert.Equal(t, int64(39000), int64(bundle.LoanTransfers[0].Principal))
	require.Len(t, bundle.LoanDisbursements, 1)
	assert.Equal(t, txnID, bundle.LoanDisbursements[0].TransactionID)
	require.Len(t, bundle.RecurringSeries, 1)
	require.Len(t, bundle.RecurringTerms, 1)
	require.Len(t, bundle.Rules, 1)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportUserDataRejectsBadFormat(t *testing.T) {
	r, _, mock := newBackupTestRouter(t)

	w := postBackup(t, r, map[string]any{"format": "something.else", "version": 1})

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportUserDataRejectsUnsupportedVersion(t *testing.T) {
	r, _, mock := newBackupTestRouter(t)

	w := postBackup(t, r, models.BackupBundle{Format: models.BackupFormat, Version: 99})

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "unsupported backup version")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportUserDataRejectsNonEmptyUser(t *testing.T) {
	r, _, mock := newBackupTestRouter(t)

	mock.ExpectBegin()
	mock.ExpectExec("SELECT id FROM users WHERE id = \\$1 FOR UPDATE").
		WithArgs(testUserID()).
		WillReturnResult(pgxmock.NewResult("SELECT", 1))
	mock.ExpectQuery("SELECT COUNT").
		WithArgs(testUserID()).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectRollback()

	bundle := models.BackupBundle{
		Format:   models.BackupFormat,
		Version:  models.BackupVersion,
		Accounts: []models.BackupAccount{{ID: uuid.New(), Name: "A", AccountTypeID: "bank"}},
	}
	w := postBackup(t, r, bundle)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportUserDataRejectsMissingAccountType(t *testing.T) {
	r, _, mock := newBackupTestRouter(t)

	mock.ExpectBegin()
	mock.ExpectExec("SELECT id FROM users WHERE id = \\$1 FOR UPDATE").
		WithArgs(testUserID()).
		WillReturnResult(pgxmock.NewResult("SELECT", 1))
	mock.ExpectQuery("SELECT COUNT").
		WithArgs(testUserID()).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT id FROM account_types").
		WithArgs(pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id"}))
	mock.ExpectRollback()

	bundle := models.BackupBundle{
		Format:   models.BackupFormat,
		Version:  models.BackupVersion,
		Accounts: []models.BackupAccount{{ID: uuid.New(), Name: "A", AccountTypeID: "crypto"}},
	}
	w := postBackup(t, r, bundle)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "account types not available")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportUserDataAccountOnly(t *testing.T) {
	r, _, mock := newBackupTestRouter(t)

	accountID := uuid.New()
	mock.ExpectBegin()
	mock.ExpectExec("SELECT id FROM users WHERE id = \\$1 FOR UPDATE").
		WithArgs(testUserID()).
		WillReturnResult(pgxmock.NewResult("SELECT", 1))
	mock.ExpectQuery("SELECT COUNT").
		WithArgs(testUserID()).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT id FROM account_types").
		WithArgs(pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("bank"))
	mock.ExpectExec("INSERT INTO accounts ").
		WithArgs(anyArgs(12)...).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	bundle := models.BackupBundle{
		Format:  models.BackupFormat,
		Version: models.BackupVersion,
		Accounts: []models.BackupAccount{
			{ID: accountID, Name: "Savings", AccountTypeID: "bank", Currency: "INR", Color: "#000000", IsDefault: true},
		},
	}
	w := postBackup(t, r, bundle)

	require.Equal(t, http.StatusOK, w.Code)

	var result models.BackupImportResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Equal(t, 1, result.Accounts)
	assert.Equal(t, 0, result.Transactions)
	assert.Empty(t, result.Warnings)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestImportUserDataRemapsReferences exercises the full dependency chain and
// verifies that the bundle's original IDs are not reused (the IDs in the
// INSERT args are freshly minted and cross-referenced).
func TestImportUserDataRemapsReferences(t *testing.T) {
	r, _, mock := newBackupTestRouter(t)

	oldAccount := uuid.New()
	oldCategory := uuid.New()
	oldPayee := uuid.New()
	oldTxn := uuid.New()

	mock.ExpectBegin()
	mock.ExpectExec("SELECT id FROM users WHERE id = \\$1 FOR UPDATE").
		WithArgs(testUserID()).
		WillReturnResult(pgxmock.NewResult("SELECT", 1))
	mock.ExpectQuery("SELECT COUNT").
		WithArgs(testUserID()).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT id FROM account_types").
		WithArgs(pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("bank"))
	mock.ExpectQuery("SELECT id FROM category_groups WHERE user_id").
		WithArgs(testUserID(), "Custom").
		WillReturnError(pgx.ErrNoRows)
	// The bundle's category references the seeded "expense" group by its slug
	// id, which the bundle does not carry: the restore verifies the target
	// instance still has it before keeping the reference.
	mock.ExpectQuery("SELECT EXISTS \\(SELECT 1 FROM category_groups").
		WithArgs("expense", testUserID()).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("SELECT id FROM categories WHERE user_id").
		WithArgs(testUserID(), "Food", "expense").
		WillReturnError(pgx.ErrNoRows)

	mock.ExpectExec("INSERT INTO accounts ").
		WithArgs(anyArgs(12)...).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO category_groups ").
		WithArgs(anyArgs(7)...).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO categories ").
		WithArgs(anyArgs(6)...).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO payees ").
		WithArgs(anyArgs(6)...).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO transactions ").
		WithArgs(anyArgs(14)...).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	bundle := models.BackupBundle{
		Format:  models.BackupFormat,
		Version: models.BackupVersion,
		Accounts: []models.BackupAccount{
			{ID: oldAccount, Name: "Savings", AccountTypeID: "bank"},
		},
		CategoryGroups: []models.BackupCategoryGroup{
			{ID: "custom", Name: "Custom", SortOrder: 1},
		},
		Categories: []models.BackupCategory{
			{ID: oldCategory, Name: "Food", GroupID: "expense"},
		},
		Payees: []models.BackupPayee{
			{ID: oldPayee, Name: "Merchant"},
		},
		Transactions: []models.BackupTransaction{
			{
				ID:          oldTxn,
				AccountID:   oldAccount,
				Date:        "2024-01-15",
				Description: "Coffee",
				Amount:      money.FromFloat(250.5),
				Type:        "debit",
				CategoryID:  &oldCategory,
				PayeeID:     &oldPayee,
			},
		},
	}

	// The exact cross-referenced UUIDs are confirmed by the integration
	// round-trip test; here the counts prove every resource was inserted in
	// dependency order without a foreign key resolving to nil.
	w := postBackup(t, r, bundle)

	require.Equal(t, http.StatusOK, w.Code)

	var result models.BackupImportResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Equal(t, 1, result.Accounts)
	assert.Equal(t, 1, result.CategoryGroups)
	assert.Equal(t, 1, result.Categories)
	assert.Equal(t, 1, result.Payees)
	assert.Equal(t, 1, result.Transactions)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportUserDataInvalidJSON(t *testing.T) {
	r, _, mock := newBackupTestRouter(t)

	req, _ := http.NewRequest("POST", "/import", bytes.NewBufferString("{"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportUserDataSkipsDanglingTransaction(t *testing.T) {
	r, _, mock := newBackupTestRouter(t)

	mock.ExpectBegin()
	mock.ExpectExec("SELECT id FROM users WHERE id = \\$1 FOR UPDATE").
		WithArgs(testUserID()).
		WillReturnResult(pgxmock.NewResult("SELECT", 1))
	mock.ExpectQuery("SELECT COUNT").
		WithArgs(testUserID()).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectCommit()

	missingAccount := uuid.New()
	bundle := models.BackupBundle{
		Format:  models.BackupFormat,
		Version: models.BackupVersion,
		Transactions: []models.BackupTransaction{
			{ID: uuid.New(), AccountID: missingAccount, Date: "2024-01-15", Description: "X", Amount: money.FromFloat(1), Type: "debit"},
		},
	}
	w := postBackup(t, r, bundle)

	require.Equal(t, http.StatusOK, w.Code)

	var result models.BackupImportResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Equal(t, 0, result.Transactions)
	require.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], "skipped transaction")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestImportUserDataFullBundle restores one row of every resource, including a
// global category resolved against the target instance and the settings update,
// so the whole dependency-ordered restore is exercised.
func TestImportUserDataFullBundle(t *testing.T) {
	r, _, mock := newBackupTestRouter(t)

	accountID := uuid.New()
	loanID := uuid.New()
	userCatID := uuid.New()
	globalCatID := uuid.New()
	payeeID := uuid.New()
	cycleID := uuid.New()
	txn1 := uuid.New()
	txn2 := uuid.New()
	seriesID := uuid.New()

	mock.ExpectBegin()
	mock.ExpectExec("SELECT id FROM users WHERE id = \\$1 FOR UPDATE").
		WithArgs(testUserID()).
		WillReturnResult(pgxmock.NewResult("SELECT", 1))
	mock.ExpectQuery("SELECT COUNT").
		WithArgs(testUserID()).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT id FROM account_types").
		WithArgs(pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("bank").AddRow("loan"))
	mock.ExpectQuery("SELECT id FROM category_groups WHERE user_id").
		WithArgs(testUserID(), "Custom").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectQuery("SELECT EXISTS \\(SELECT 1 FROM category_groups").
		WithArgs("expense", testUserID()).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("SELECT id FROM categories WHERE user_id").
		WithArgs(testUserID(), "Food", "expense").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectQuery("SELECT EXISTS \\(SELECT 1 FROM category_groups").
		WithArgs("expense", testUserID()).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("SELECT id FROM categories WHERE user_id IS NULL").
		WithArgs("GlobalCat", "expense").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(uuid.New()))

	mock.ExpectExec("INSERT INTO accounts ").WithArgs(anyArgs(24)...).WillReturnResult(pgxmock.NewResult("INSERT", 2))
	mock.ExpectExec("INSERT INTO category_groups ").WithArgs(anyArgs(7)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO categories ").WithArgs(anyArgs(6)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO payees ").WithArgs(anyArgs(6)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO billing_cycles ").WithArgs(anyArgs(7)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO transactions ").WithArgs(anyArgs(28)...).WillReturnResult(pgxmock.NewResult("INSERT", 2))
	mock.ExpectExec("INSERT INTO links ").WithArgs(anyArgs(7)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO loan_attachments ").WithArgs(anyArgs(5)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO loan_schedules ").WithArgs(anyArgs(11)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO loan_transfers ").WithArgs(anyArgs(9)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO loan_disbursements ").WithArgs(anyArgs(5)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO recurring_series ").WithArgs(anyArgs(13)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO recurring_series_terms ").WithArgs(anyArgs(8)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO recurring_attachments ").WithArgs(anyArgs(5)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO rules ").WithArgs(anyArgs(19)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("UPDATE users SET").WithArgs(anyArgs(4)...).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	bundle := models.BackupBundle{
		Format:   models.BackupFormat,
		Version:  models.BackupVersion,
		Settings: &models.BackupSettings{PaperlessURL: "http://p", PaperlessTag: "t", PageSize: intPtr(25)},
		Accounts: []models.BackupAccount{
			{ID: accountID, Name: "Savings", AccountTypeID: "bank"},
			{ID: loanID, Name: "Loan", AccountTypeID: "loan"},
		},
		CategoryGroups: []models.BackupCategoryGroup{{ID: "custom", Name: "Custom", SortOrder: 1}},
		Categories: []models.BackupCategory{
			{ID: userCatID, Name: "Food", GroupID: "expense"},
			{ID: globalCatID, Name: "GlobalCat", GroupID: "expense", Global: true},
		},
		Payees:        []models.BackupPayee{{ID: payeeID, Name: "Merchant"}},
		BillingCycles: []models.BackupBillingCycle{{ID: cycleID, AccountID: accountID, StartDate: "2024-01-01", EndDate: "2024-01-31", Label: "Jan"}},
		Transactions: []models.BackupTransaction{
			{ID: txn1, AccountID: accountID, Date: "2024-01-15", Description: "Coffee", Amount: money.FromFloat(250.5), Type: "debit", CategoryID: &userCatID, PayeeID: &payeeID, BillingCycleID: &cycleID},
			{ID: txn2, AccountID: accountID, Date: "2024-01-16", Description: "Global", Amount: money.FromFloat(100), Type: "debit", CategoryID: &globalCatID},
		},
		Links:           []models.BackupLink{{ID: uuid.New(), Type: "transfer", FromTxnID: txn1, ToTxnID: txn2}},
		LoanAttachments: []models.BackupLoanAttachment{{ID: uuid.New(), LoanAccountID: loanID, TransactionID: txn1}},
		LoanSchedules: []models.BackupLoanSchedule{{
			ID: uuid.New(), LoanAccountID: loanID, Principal: money.FromFloat(1000),
			ProcessingFee: money.FromFloat(25), AnnualRateBps: 900, TenureMonths: 24, StartDate: "2024-02-01",
		}},
		LoanTransfers: []models.BackupLoanTransfer{{
			ID: uuid.New(), FromLoanAccountID: loanID, ToLoanAccountID: accountID,
			Amount: money.FromFloat(400), Principal: money.FromFloat(380),
			TransferDate: "2024-03-01", Mode: models.LoanTransferTakeover,
		}},
		LoanDisbursements: []models.BackupLoanDisbursement{{
			ID: uuid.New(), LoanAccountID: loanID, TransactionID: txn1,
		}},
		RecurringSeries: []models.BackupRecurringSeries{{
			ID: seriesID, Name: "Rent",
			Type: "debit", Frequency: "monthly", Interval: 1,
			CategoryID: &userCatID, PayeeID: &payeeID, Active: true,
		}},
		RecurringTerms: []models.BackupRecurringTerm{{
			ID: uuid.New(), SeriesID: seriesID, StartDate: "2024-01-01", Amount: money.FromFloat(1000), AccountID: accountID,
		}},
		RecurringAttachments: []models.BackupRecurringAttachment{{ID: uuid.New(), SeriesID: seriesID, TransactionID: txn2}},
		Rules: []models.BackupRule{{
			ID: uuid.New(), Pattern: "coffee", MatchType: "contains", CategoryID: userCatID, PayeeID: &payeeID, Priority: 1,
		}},
	}

	w := postBackup(t, r, bundle)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var result models.BackupImportResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Equal(t, 2, result.Accounts)
	assert.Equal(t, 1, result.CategoryGroups)
	assert.Equal(t, 1, result.Categories)
	assert.Equal(t, 1, result.Payees)
	assert.Equal(t, 1, result.BillingCycles)
	assert.Equal(t, 2, result.Transactions)
	assert.Equal(t, 1, result.Links)
	assert.Equal(t, 1, result.LoanAttachments)
	assert.Equal(t, 1, result.LoanDisbursements)
	assert.Equal(t, 1, result.RecurringSeries)
	assert.Equal(t, 1, result.RecurringTerms)
	assert.Equal(t, 1, result.RecurringAttachments)
	assert.Equal(t, 1, result.Rules)
	assert.Empty(t, result.Warnings)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestExportUserDataQueryError(t *testing.T) {
	r, _, mock := newBackupTestRouter(t)

	mock.ExpectQuery("FROM users WHERE id").
		WithArgs(testUserID()).
		WillReturnError(pgx.ErrNoRows)

	req, _ := http.NewRequest("GET", "/export", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// mergeBackupPayees is what keeps a bundle written before the
// payees_user_name_uq index from failing the whole restore on the payees
// insert, so it must fold duplicates onto a survivor that every reference can
// point at — and must never drop the payee an account is linked to.
func TestMergeBackupPayees(t *testing.T) {
	t.Run("leaves distinct payees alone", func(t *testing.T) {
		payees := []models.BackupPayee{
			{ID: uuid.New(), Name: "Amazon"},
			{ID: uuid.New(), Name: "Airtel"},
		}

		kept, folded := mergeBackupPayees(payees)

		assert.Equal(t, payees, kept)
		assert.Empty(t, folded)
	})

	t.Run("folds a duplicate name onto the first row", func(t *testing.T) {
		first := models.BackupPayee{ID: uuid.New(), Name: "Coffee Shop"}
		second := models.BackupPayee{ID: uuid.New(), Name: "Coffee Shop"}

		kept, folded := mergeBackupPayees([]models.BackupPayee{first, second})

		assert.Equal(t, []models.BackupPayee{first}, kept)
		assert.Equal(t, map[uuid.UUID]uuid.UUID{second.ID: first.ID}, folded)
	})

	t.Run("keeps the account-linked row as the survivor", func(t *testing.T) {
		manual := models.BackupPayee{ID: uuid.New(), Name: "HDFC"}
		accountID := uuid.New()
		linked := models.BackupPayee{ID: uuid.New(), Name: "HDFC", AccountID: &accountID}

		kept, folded := mergeBackupPayees([]models.BackupPayee{manual, linked})

		require.Len(t, kept, 1)
		assert.Equal(t, linked.ID, kept[0].ID)
		assert.Equal(t, &accountID, kept[0].AccountID)
		assert.Equal(t, map[uuid.UUID]uuid.UUID{manual.ID: linked.ID}, folded)
	})

	t.Run("disambiguates a second account-linked row instead of dropping it", func(t *testing.T) {
		firstAccount, secondAccount := uuid.New(), uuid.New()
		first := models.BackupPayee{ID: uuid.New(), Name: "Savings", AccountID: &firstAccount}
		second := models.BackupPayee{ID: uuid.New(), Name: "Savings", AccountID: &secondAccount}

		kept, folded := mergeBackupPayees([]models.BackupPayee{first, second})

		require.Len(t, kept, 2)
		assert.Equal(t, first, kept[0])
		assert.Equal(t, "Savings ("+second.ID.String()[:8]+")", kept[1].Name)
		assert.Equal(t, &secondAccount, kept[1].AccountID)
		assert.Empty(t, folded)
	})

	t.Run("folds every duplicate of a name onto one survivor", func(t *testing.T) {
		accountID := uuid.New()
		linked := models.BackupPayee{ID: uuid.New(), Name: "Zomato", AccountID: &accountID}
		firstManual := models.BackupPayee{ID: uuid.New(), Name: "Zomato"}
		lastManual := models.BackupPayee{ID: uuid.New(), Name: "Zomato"}

		kept, folded := mergeBackupPayees([]models.BackupPayee{firstManual, linked, lastManual})

		assert.Equal(t, []models.BackupPayee{linked}, kept)
		assert.Equal(t, map[uuid.UUID]uuid.UUID{
			firstManual.ID: linked.ID,
			lastManual.ID:  linked.ID,
		}, folded)
	})

	t.Run("truncates a long name instead of overflowing the column", func(t *testing.T) {
		// payees.name is VARCHAR(255) and the suffix adds 11 characters: without
		// the truncation the payees INSERT would fail and take the whole
		// all-or-nothing restore with it.
		firstAccount, secondAccount := uuid.New(), uuid.New()
		long := strings.Repeat("n", 250)
		first := models.BackupPayee{ID: uuid.New(), Name: long, AccountID: &firstAccount}
		second := models.BackupPayee{ID: uuid.New(), Name: long, AccountID: &secondAccount}

		kept, _ := mergeBackupPayees([]models.BackupPayee{first, second})

		require.Len(t, kept, 2)
		assert.Len(t, []rune(kept[1].Name), 255)
		assert.True(t, strings.HasSuffix(kept[1].Name, "("+second.ID.String()[:8]+")"))
	})
}

// expectRestorePrelude registers the statements every restore runs before it
// reaches the row loops: the per-user lock, the emptiness check, and the
// account-type check (the bundles below all carry one "bank" account).
func expectRestorePrelude(mock pgxmock.PgxPoolIface) {
	mock.ExpectBegin()
	mock.ExpectExec("SELECT id FROM users WHERE id = \\$1 FOR UPDATE").
		WithArgs(testUserID()).
		WillReturnResult(pgxmock.NewResult("SELECT", 1))
	mock.ExpectQuery("SELECT COUNT").
		WithArgs(testUserID()).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT id FROM account_types").
		WithArgs(pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("bank"))
}

// expectSeededExpenseGroup registers the group check and category lookup a
// bundle category in the seeded "expense" group costs, resolving to a category
// the target user does not have yet.
func expectSeededExpenseGroup(mock pgxmock.PgxPoolIface) {
	mock.ExpectQuery("SELECT EXISTS \\(SELECT 1 FROM category_groups").
		WithArgs("expense", testUserID()).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("SELECT id FROM categories WHERE user_id").
		WithArgs(testUserID(), "Food", "expense").
		WillReturnError(pgx.ErrNoRows)
}

// mappedUUID matches a rule reference the restore resolved to a fresh id.
// pgxmock.OfType would not do: it also matches the typed nil the restore inserts
// for a reference it could not resolve.
func mappedUUID() pgxmock.Argument {
	return pgxmock.ArgumentFunc(func(v any) bool {
		p, ok := v.(*uuid.UUID)
		return ok && p != nil
	})
}

// clearedUUID matches the typed nil pointer the restore inserts when it cannot
// resolve a reference. A plain nil expectation would not match: pgxmock compares
// non-matcher arguments with reflect.DeepEqual, and the value is (*uuid.UUID)(nil).
func clearedUUID() pgxmock.Argument {
	return pgxmock.ArgumentFunc(func(v any) bool {
		p, ok := v.(*uuid.UUID)
		return ok && p == nil
	})
}

// oneRuleBundle is a bundle holding one account, one category in the seeded
// "expense" group, and one rule whose category filter points at filterCategoryID.
func oneRuleBundle(accountID, categoryID uuid.UUID, filterCategoryID *uuid.UUID) models.BackupBundle {
	return models.BackupBundle{
		Format:     models.BackupFormat,
		Version:    models.BackupVersion,
		Accounts:   []models.BackupAccount{{ID: accountID, Name: "A", AccountTypeID: "bank"}},
		Categories: []models.BackupCategory{{ID: categoryID, Name: "Food", GroupID: "expense"}},
		Rules: []models.BackupRule{{
			ID: uuid.New(), Pattern: "coffee", MatchType: "contains",
			CategoryID: categoryID, FilterCategoryID: filterCategoryID, Priority: 1,
		}},
	}
}

// expectRestoreOfOneRule registers the statements restoring oneRuleBundle runs.
// filterArg is the matcher for the rule INSERT's filter_category_id column
// (the ninth of its nineteen arguments).
func expectRestoreOfOneRule(mock pgxmock.PgxPoolIface, filterArg any) {
	expectRestorePrelude(mock)
	expectSeededExpenseGroup(mock)
	mock.ExpectExec("INSERT INTO accounts ").WithArgs(anyArgs(12)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO categories ").WithArgs(anyArgs(6)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	ruleArgs := anyArgs(19)
	ruleArgs[8] = filterArg
	mock.ExpectExec("INSERT INTO rules ").WithArgs(ruleArgs...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()
}

// TestImportUserDataRestoresRuleFilterCategory pins the round trip of a rule's
// category *filter*: the bundle now carries the category the filter references
// and the restore maps the reference onto it, so the condition survives.
func TestImportUserDataRestoresRuleFilterCategory(t *testing.T) {
	r, _, mock := newBackupTestRouter(t)

	accountID, categoryID := uuid.New(), uuid.New()
	expectRestoreOfOneRule(mock, mappedUUID())

	w := postBackup(t, r, oneRuleBundle(accountID, categoryID, &categoryID))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var result models.BackupImportResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Equal(t, 1, result.Rules)
	assert.Empty(t, result.Warnings)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestImportUserDataWarnsWhenRuleFilterCategoryIsMissing pins that a filter the
// bundle does not carry is reported instead of degrading silently: the restored
// rule has no category condition left, so it matches every transaction in its
// other dimensions.
func TestImportUserDataWarnsWhenRuleFilterCategoryIsMissing(t *testing.T) {
	r, _, mock := newBackupTestRouter(t)

	accountID, categoryID := uuid.New(), uuid.New()
	missingCategory := uuid.New()
	expectRestoreOfOneRule(mock, clearedUUID())

	w := postBackup(t, r, oneRuleBundle(accountID, categoryID, &missingCategory))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var result models.BackupImportResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Equal(t, 1, result.Rules)
	require.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], "cleared a rule's category filter")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestImportUserDataRollsUpFallbackGroupWarnings pins that categories moved into
// the fallback group are reported once with their count. The per-category form
// repeated identical text and, with more than maxBackupWarnings such categories,
// exhausted the budget and silently discarded the warnings that name what the
// import actually dropped.
func TestImportUserDataRollsUpFallbackGroupWarnings(t *testing.T) {
	r, _, mock := newBackupTestRouter(t)

	accountID := uuid.New()
	const missingGroup = "admin-created-group"

	expectRestorePrelude(mock)
	// Expectations are matched in order, so the three categories register the
	// calls they actually make: none of their groups is in the bundle or on this
	// instance, the fallback group is looked up and created once (on the first
	// category), and each category then resolves to a new row.
	for i := range 3 {
		mock.ExpectQuery("SELECT EXISTS \\(SELECT 1 FROM category_groups").
			WithArgs(missingGroup, testUserID()).
			WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
		if i == 0 {
			mock.ExpectQuery("SELECT id FROM category_groups WHERE user_id = \\$1 AND name = \\$2").
				WithArgs(testUserID(), "Imported").
				WillReturnError(pgx.ErrNoRows)
		}
		mock.ExpectQuery("SELECT id FROM categories WHERE user_id").
			WithArgs(testUserID(), pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnError(pgx.ErrNoRows)
	}
	mock.ExpectExec("INSERT INTO accounts ").WithArgs(anyArgs(12)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO category_groups ").WithArgs(anyArgs(7)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO categories ").WithArgs(anyArgs(18)...).WillReturnResult(pgxmock.NewResult("INSERT", 3))
	mock.ExpectCommit()

	categories := make([]models.BackupCategory, 0, 3)
	for i := range 3 {
		categories = append(categories, models.BackupCategory{
			ID: uuid.New(), Name: fmt.Sprintf("C%d", i), GroupID: missingGroup,
		})
	}
	bundle := models.BackupBundle{
		Format:     models.BackupFormat,
		Version:    models.BackupVersion,
		Accounts:   []models.BackupAccount{{ID: accountID, Name: "A", AccountTypeID: "bank"}},
		Categories: categories,
	}

	w := postBackup(t, r, bundle)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var result models.BackupImportResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Equal(t, 3, result.Categories)
	require.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], `moved 3 categories into the "Imported" group`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestAddBackupWarningFlagsTruncation pins that the warning budget is bounded and
// that reaching it is visible: a truncated list must not look like a complete
// account of what an import dropped.
func TestAddBackupWarningFlagsTruncation(t *testing.T) {
	res := &models.BackupImportResult{}

	for i := range maxBackupWarnings + 5 {
		addBackupWarning(res, fmt.Sprintf("warning %d", i))
	}

	require.Len(t, res.Warnings, maxBackupWarnings+1)
	assert.Equal(t, fmt.Sprintf("warning %d", maxBackupWarnings-1), res.Warnings[maxBackupWarnings-1])
	assert.Contains(t, res.Warnings[maxBackupWarnings], "further warnings omitted")

	// The notice is appended once, not once per discarded warning.
	addBackupWarning(res, "one warning too many")
	assert.Len(t, res.Warnings, maxBackupWarnings+1)
}

// TestImportUserDataRejectsMalformedBundle pins that a bundle carrying values the
// schema would reject answers 400 with the offending field named, instead of
// aborting the transaction and reporting an internal error. Nothing is inserted.
func TestImportUserDataRejectsMalformedBundle(t *testing.T) {
	accountID := uuid.New()
	categoryID := uuid.New()

	account := func() []models.BackupAccount {
		return []models.BackupAccount{{ID: accountID, Name: "A", AccountTypeID: "bank"}}
	}

	cases := []struct {
		name   string
		bundle models.BackupBundle
		want   string
		// group, when true, expects the bundle's category in the seeded group (the
		// rule cases below carry one).
		group bool
	}{
		{
			name: "transaction type outside the CHECK constraint",
			bundle: models.BackupBundle{
				Format: models.BackupFormat, Version: models.BackupVersion,
				Accounts: account(),
				Transactions: []models.BackupTransaction{{
					ID: uuid.New(), AccountID: accountID, Date: "2024-01-15",
					Description: "x", Amount: money.FromFloat(10), Type: "DEBIT",
				}},
			},
			want: "invalid transaction type",
		},
		{
			name: "transaction amount beyond any plausible figure",
			bundle: models.BackupBundle{
				Format: models.BackupFormat, Version: models.BackupVersion,
				Accounts: account(),
				Transactions: []models.BackupTransaction{{
					ID: uuid.New(), AccountID: accountID, Date: "2024-01-15",
					Description: "x", Amount: money.Amount(1_100_000_000_000_000), Type: "debit",
				}},
			},
			want: "invalid amount in backup",
		},
		{
			name: "recurring frequency outside the CHECK constraint",
			bundle: models.BackupBundle{
				Format: models.BackupFormat, Version: models.BackupVersion,
				Accounts: account(),
				RecurringSeries: []models.BackupRecurringSeries{{
					ID: uuid.New(), Name: "Rent", Type: "debit", Frequency: "fortnightly", Interval: 1,
				}},
			},
			want: "invalid recurring frequency",
		},
		{
			name: "rule date is a timestamp, not a date",
			bundle: models.BackupBundle{
				Format: models.BackupFormat, Version: models.BackupVersion,
				Accounts:   account(),
				Categories: []models.BackupCategory{{ID: categoryID, Name: "Food", GroupID: "expense"}},
				Rules: []models.BackupRule{{
					ID: uuid.New(), Pattern: "coffee", MatchType: "contains",
					CategoryID: categoryID, DateFrom: new("2024-01-15T00:00:00Z"),
				}},
			},
			want:  "invalid date",
			group: true,
		},
		{
			name: "rule match type outside the CHECK constraint",
			bundle: models.BackupBundle{
				Format: models.BackupFormat, Version: models.BackupVersion,
				Accounts:   account(),
				Categories: []models.BackupCategory{{ID: categoryID, Name: "Food", GroupID: "expense"}},
				Rules: []models.BackupRule{{
					ID: uuid.New(), Pattern: "coffee", MatchType: "regex", CategoryID: categoryID,
				}},
			},
			want:  "invalid rule match type",
			group: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, _, mock := newBackupTestRouter(t)
			expectRestorePrelude(mock)
			if tc.group {
				expectSeededExpenseGroup(mock)
			}
			// The restore is all-or-nothing: a rejected bundle must roll back
			// without a single INSERT.
			mock.ExpectRollback()

			w := postBackup(t, r, tc.bundle)

			assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), tc.want)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// TestValidateBackupAmountRejectsTheMinIntSentinel covers the value the JSON
// decoder used to produce for an out-of-range or NaN amount. It cannot be built
// through the HTTP endpoint (money.Amount.String then emits invalid JSON, so the
// bundle cannot even be marshalled for the request), which is exactly why the
// restore has to refuse it before it reaches a row.
func TestValidateBackupAmountRejectsTheMinIntSentinel(t *testing.T) {
	assert.Error(t, validateBackupAmount("amount", money.Amount(math.MinInt64)))

	// The bounds themselves are inclusive; ordinary money is untouched.
	assert.NoError(t, validateBackupAmount("amount", maxBackupAmount))
	assert.NoError(t, validateBackupAmount("amount", -maxBackupAmount))
	assert.Error(t, validateBackupAmount("amount", maxBackupAmount+1))
	assert.Error(t, validateBackupAmount("amount", -maxBackupAmount-1))
	assert.NoError(t, validateBackupAmount("amount", money.FromFloat(1250.5)))
	assert.NoError(t, validateBackupAmount("amount", 0))
}

// TestParseBackupDatePtr covers the optional-date helper the rule rows use: an
// absent date stays NULL, a present one is parsed (and rejected when malformed).
func TestParseBackupDatePtr(t *testing.T) {
	got, err := parseBackupDatePtr(nil)
	require.NoError(t, err)
	assert.Nil(t, got)

	got, err = parseBackupDatePtr(new("2024-02-29"))
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "2024-02-29", got.Format("2006-01-02"))

	_, err = parseBackupDatePtr(new("2024-02-30"))
	var be *backupError
	require.ErrorAs(t, err, &be)
	assert.Equal(t, http.StatusBadRequest, be.status)
}
