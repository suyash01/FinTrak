package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/fintrak/backend/internal/money"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
)

func TestUpdateTransactionClearsCategory(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.PATCH("/transactions/:id", srv.UpdateTransaction)

	txnID := uuid.New()
	userID := testUserID()

	mock.ExpectExec("UPDATE transactions SET category_id").
		WithArgs(nil, txnID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	req, _ := http.NewRequest("PATCH", "/transactions/"+txnID.String(), bytes.NewBufferString(`{"categoryId":null}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateTransactionCategoryAbsent(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.PATCH("/transactions/:id", srv.UpdateTransaction)

	txnID := uuid.New()

	// No category field present -> should hit "no fields to update".
	req, _ := http.NewRequest("PATCH", "/transactions/"+txnID.String(), bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateTransactionSetsCategory(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.PATCH("/transactions/:id", srv.UpdateTransaction)

	txnID := uuid.New()
	catID := uuid.New()
	userID := testUserID()

	mock.ExpectExec("UPDATE transactions SET category_id").
		WithArgs(catID, txnID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	body, _ := json.Marshal(map[string]interface{}{"categoryId": catID})
	req, _ := http.NewRequest("PATCH", "/transactions/"+txnID.String(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateTransactionSetsBillingCycle(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.PATCH("/transactions/:id", srv.UpdateTransaction)

	txnID := uuid.New()
	cycleID := uuid.New()
	userID := testUserID()

	mock.ExpectExec("UPDATE transactions SET billing_cycle_id").
		WithArgs(cycleID, txnID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	body, _ := json.Marshal(map[string]interface{}{"billingCycleId": cycleID})
	req, _ := http.NewRequest("PATCH", "/transactions/"+txnID.String(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateTransactionClearsBillingCycle(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.PATCH("/transactions/:id", srv.UpdateTransaction)

	txnID := uuid.New()
	userID := testUserID()

	mock.ExpectExec("UPDATE transactions SET billing_cycle_id").
		WithArgs(nil, txnID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	body, _ := json.Marshal(map[string]interface{}{"billingCycleId": nil})
	req, _ := http.NewRequest("PATCH", "/transactions/"+txnID.String(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Moving a transaction to another account must not leave it attached to the
// old account's cycle: cycles belong to one account, and the billing-cycle
// rollups aggregate by cycle id without rechecking the account, so a stale
// cycle would pollute the old cycle's net. Naming no cycle binds no parameter,
// so the id/user placeholders after it stay where they were.
func TestUpdateTransactionAccountMoveClearsBillingCycle(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.PATCH("/transactions/:id", srv.UpdateTransaction)

	userID := testUserID()
	txnID := uuid.New()
	accountID := uuid.New()

	mock.ExpectExec("UPDATE transactions SET account_id = \\$1, billing_cycle_id = NULL WHERE id = \\$2 AND user_id = \\$3").
		WithArgs(accountID, txnID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	body, _ := json.Marshal(map[string]interface{}{"accountId": accountID})
	req, _ := http.NewRequest("PATCH", "/transactions/"+txnID.String(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateTransactionCreditCardAutoAssign(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.POST("/transactions", srv.CreateTransaction)

	userID := testUserID()
	accountID := uuid.New()
	catID := uuid.New()
	txnID := uuid.New()

	reqBody := models.CreateTransactionRequest{
		AccountID:   accountID,
		Date:        "2024-01-15",
		Description: "Coffee",
		Amount:      money.FromFloat(250.5),
		Type:        "debit",
		CategoryID:  &catID,
	}

	mock.ExpectBegin()

	// Account ownership check (credit card).
	mock.ExpectQuery("SELECT user_id, billing_day").
		WithArgs(accountID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "billing_day", "closed", "account_type_id"}).AddRow(userID, intPtr(5), false, "bank"))

	// Insert.
	mock.ExpectQuery("INSERT INTO transactions").
		WithArgs(accountID, userID, "2024-01-15", "Coffee", money.FromFloat(250.5), "debit", &catID, (*uuid.UUID)(nil), []string(nil), "").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(txnID))

	// ensureBillingCycles: alignment check (no stale cycles).
	mock.ExpectQuery("SELECT end_date FROM billing_cycles").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"end_date"}))

	// ensureBillingCycles: earliest transaction.
	mock.ExpectQuery("MIN\\(date\\)").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"min"}).AddRow(time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)))

	// ensureBillingCycles: every month already has a cycle -> nothing to generate.
	covered := pgxmock.NewRows([]string{"end_date"})
	for _, ms := range billingCycleMonths(time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC), dateOnly(time.Now()), 5) {
		_, end := cycleDates(ms, 5)
		covered.AddRow(end)
	}
	mock.ExpectQuery("SELECT end_date FROM billing_cycles").
		WithArgs(accountID, userID).
		WillReturnRows(covered)

	// ensureBillingCycles: unassigned exists -> back-fill (attaches this
	// transaction by date).
	mock.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM transactions WHERE account_id").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectExec("UPDATE transactions t SET billing_cycle_id").
		WithArgs(accountID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	mock.ExpectCommit()

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/transactions", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateTransactionCreditCardExplicitCycle(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.POST("/transactions", srv.CreateTransaction)

	userID := testUserID()
	accountID := uuid.New()
	catID := uuid.New()
	cycleID := uuid.New()
	txnID := uuid.New()

	reqBody := models.CreateTransactionRequest{
		AccountID:      accountID,
		Date:           "2024-01-15",
		Description:    "Coffee",
		Amount:         money.FromFloat(250.5),
		Type:           "debit",
		CategoryID:     &catID,
		BillingCycleID: &cycleID,
	}

	mock.ExpectBegin()

	// Account ownership check (credit card).
	mock.ExpectQuery("SELECT user_id, billing_day").
		WithArgs(accountID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "billing_day", "closed", "account_type_id"}).AddRow(userID, intPtr(5), false, "bank"))

	// Explicit billing cycle ownership + account-match check.
	mock.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM billing_cycles").
		WithArgs(cycleID, userID, accountID).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))

	// Insert.
	mock.ExpectQuery("INSERT INTO transactions").
		WithArgs(accountID, userID, "2024-01-15", "Coffee", money.FromFloat(250.5), "debit", &catID, (*uuid.UUID)(nil), []string(nil), "").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(txnID))

	// ensureBillingCycles: alignment check (no stale cycles).
	mock.ExpectQuery("SELECT end_date FROM billing_cycles").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"end_date"}))

	// ensureBillingCycles: earliest transaction.
	mock.ExpectQuery("MIN\\(date\\)").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"min"}).AddRow(time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)))

	// ensureBillingCycles: every month already has a cycle -> nothing to generate.
	covered := pgxmock.NewRows([]string{"end_date"})
	for _, ms := range billingCycleMonths(time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC), dateOnly(time.Now()), 5) {
		_, end := cycleDates(ms, 5)
		covered.AddRow(end)
	}
	mock.ExpectQuery("SELECT end_date FROM billing_cycles").
		WithArgs(accountID, userID).
		WillReturnRows(covered)

	// ensureBillingCycles: unassigned exists -> back-fill.
	mock.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM transactions WHERE account_id").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectExec("UPDATE transactions t SET billing_cycle_id").
		WithArgs(accountID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	// Explicit cycle override.
	mock.ExpectExec("UPDATE transactions SET billing_cycle_id").
		WithArgs(cycleID, txnID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	mock.ExpectCommit()

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/transactions", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func newImportTestRouter(t *testing.T) (*gin.Engine, *Server, pgxmock.PgxPoolIface) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(mock)
	t.Cleanup(mock.Close)

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.POST("/transactions/import", srv.ImportTransactions)
	return r, srv, mock
}

func postImport(r *gin.Engine, body []byte) *httptest.ResponseRecorder {
	req, _ := http.NewRequest("POST", "/transactions/import", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestImportTransactionsValidatesPayload(t *testing.T) {
	accountID := uuid.New()

	valid := models.ImportTransaction{
		Date:        "2024-01-15",
		Description: "Coffee",
		Amount:      money.FromFloat(250.5),
		Type:        "debit",
	}

	tooMany := make([]models.ImportTransaction, maxImportBatch+1)
	for i := range tooMany {
		tooMany[i] = valid
	}

	tests := []struct {
		name    string
		request models.ImportRequest
	}{
		{name: "empty transactions", request: models.ImportRequest{AccountID: accountID}},
		{name: "too many transactions", request: models.ImportRequest{AccountID: accountID, Transactions: tooMany}},
		{name: "invalid duplicate action", request: models.ImportRequest{AccountID: accountID, Transactions: []models.ImportTransaction{valid}, DuplicateAction: "maybe"}},
		{name: "invalid type", request: models.ImportRequest{AccountID: accountID, Transactions: []models.ImportTransaction{{Date: "2024-01-15", Description: "X", Amount: money.FromFloat(1), Type: "refund"}}}},
		{name: "invalid amount", request: models.ImportRequest{AccountID: accountID, Transactions: []models.ImportTransaction{{Date: "2024-01-15", Description: "X", Amount: money.FromFloat(0), Type: "debit"}}}},
		{name: "invalid date", request: models.ImportRequest{AccountID: accountID, Transactions: []models.ImportTransaction{{Date: "15/01/2024", Description: "X", Amount: money.FromFloat(1), Type: "debit"}}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _, mock := newImportTestRouter(t)

			body, _ := json.Marshal(tt.request)
			w := postImport(r, body)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestImportTransactionsAccountNotFound(t *testing.T) {
	r, _, mock := newImportTestRouter(t)

	accountID := uuid.New()
	mock.ExpectQuery("SELECT user_id, billing_day").
		WithArgs(accountID).
		WillReturnError(pgx.ErrNoRows)

	body, _ := json.Marshal(models.ImportRequest{
		AccountID:    accountID,
		Transactions: []models.ImportTransaction{{Date: "2024-01-15", Description: "X", Amount: money.FromFloat(1), Type: "debit"}},
	})
	w := postImport(r, body)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportTransactionsAccountForbidden(t *testing.T) {
	r, _, mock := newImportTestRouter(t)

	accountID := uuid.New()
	mock.ExpectQuery("SELECT user_id, billing_day").
		WithArgs(accountID).
		WillReturnRows(mock.NewRows([]string{"user_id", "billing_day", "closed", "account_type_id"}).AddRow(uuid.New(), nil, false, "bank"))

	body, _ := json.Marshal(models.ImportRequest{
		AccountID:    accountID,
		Transactions: []models.ImportTransaction{{Date: "2024-01-15", Description: "X", Amount: money.FromFloat(1), Type: "debit"}},
	})
	w := postImport(r, body)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func newValidateTestRouter(t *testing.T) (*gin.Engine, *Server, pgxmock.PgxPoolIface) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(mock)
	t.Cleanup(mock.Close)

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.POST("/transactions/validate", srv.ValidateTransactions)
	return r, srv, mock
}

func postValidate(r *gin.Engine, body []byte) *httptest.ResponseRecorder {
	req, _ := http.NewRequest("POST", "/transactions/validate", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestValidateTransactionsValidatesPayload(t *testing.T) {
	accountID := uuid.New()

	valid := models.ImportTransaction{
		Date:        "2024-01-15",
		Description: "Coffee",
		Amount:      money.FromFloat(250.5),
		Type:        "debit",
	}

	tooMany := make([]models.ImportTransaction, maxImportBatch+1)
	for i := range tooMany {
		tooMany[i] = valid
	}

	tests := []struct {
		name    string
		request models.ValidateTransactionsRequest
	}{
		{name: "empty transactions", request: models.ValidateTransactionsRequest{AccountID: accountID}},
		{name: "too many transactions", request: models.ValidateTransactionsRequest{AccountID: accountID, Transactions: tooMany}},
		{name: "invalid type", request: models.ValidateTransactionsRequest{AccountID: accountID, Transactions: []models.ImportTransaction{{Date: "2024-01-15", Description: "X", Amount: money.FromFloat(1), Type: "refund"}}}},
		{name: "invalid amount", request: models.ValidateTransactionsRequest{AccountID: accountID, Transactions: []models.ImportTransaction{{Date: "2024-01-15", Description: "X", Amount: money.FromFloat(0), Type: "debit"}}}},
		{name: "invalid date", request: models.ValidateTransactionsRequest{AccountID: accountID, Transactions: []models.ImportTransaction{{Date: "15/01/2024", Description: "X", Amount: money.FromFloat(1), Type: "debit"}}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _, mock := newValidateTestRouter(t)

			body, _ := json.Marshal(tt.request)
			w := postValidate(r, body)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestValidateTransactionsAccountNotFound(t *testing.T) {
	r, _, mock := newValidateTestRouter(t)

	accountID := uuid.New()
	mock.ExpectQuery("SELECT user_id FROM accounts").
		WithArgs(accountID).
		WillReturnError(pgx.ErrNoRows)

	body, _ := json.Marshal(models.ValidateTransactionsRequest{
		AccountID:    accountID,
		Transactions: []models.ImportTransaction{{Date: "2024-01-15", Description: "X", Amount: money.FromFloat(1), Type: "debit"}},
	})
	w := postValidate(r, body)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestValidateTransactionsAccountForbidden(t *testing.T) {
	r, _, mock := newValidateTestRouter(t)

	accountID := uuid.New()
	mock.ExpectQuery("SELECT user_id FROM accounts").
		WithArgs(accountID).
		WillReturnRows(mock.NewRows([]string{"user_id"}).AddRow(uuid.New()))

	body, _ := json.Marshal(models.ValidateTransactionsRequest{
		AccountID:    accountID,
		Transactions: []models.ImportTransaction{{Date: "2024-01-15", Description: "X", Amount: money.FromFloat(1), Type: "debit"}},
	})
	w := postValidate(r, body)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestValidateTransactionsSuccess(t *testing.T) {
	r, _, mock := newValidateTestRouter(t)

	accountID := uuid.New()
	userID := testUserID()

	mock.ExpectQuery("SELECT user_id FROM accounts").
		WithArgs(accountID).
		WillReturnRows(mock.NewRows([]string{"user_id"}).AddRow(userID))

	// One existing transaction that matches the first candidate, and one that
	// does not match anything. The lookup is scoped to the batch's distinct
	// dates (sorted ascending).
	existingDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT date, amount, type, description FROM transactions").
		WithArgs(accountID, userID, []time.Time{
			time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
			time.Date(2024, 1, 16, 0, 0, 0, 0, time.UTC),
		}).
		WillReturnRows(mock.NewRows([]string{"date", "amount", "type", "description"}).
			AddRow(existingDate, 250.5, "debit", "Coffee").
			AddRow(existingDate, 99.0, "credit", "Cashback"))

	body, _ := json.Marshal(models.ValidateTransactionsRequest{
		AccountID: accountID,
		Transactions: []models.ImportTransaction{
			{Date: "2024-01-15", Description: "Coffee", Amount: money.FromFloat(250.5), Type: "debit"},    // exists
			{Date: "2024-01-16", Description: "Groceries", Amount: money.FromFloat(120.0), Type: "debit"}, // new
		},
	})
	w := postValidate(r, body)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp models.ValidateTransactionsResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, 2, resp.Total)
	assert.Equal(t, 1, resp.ExistingCount)
	assert.Equal(t, 1, resp.MissingCount)
	assert.Len(t, resp.Results, 2)
	assert.True(t, resp.Results[0].Exists)
	assert.False(t, resp.Results[1].Exists)
	assert.Equal(t, 0, resp.Results[0].Index)
	assert.Equal(t, 1, resp.Results[1].Index)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// The import success path uses pgx.Batch (tx.SendBatch), which pgxmock does not
// support (it returns nil), so the full handler can't be exercised here. The
// billing-cycle override it calls is covered directly by
// TestAttachTransactionsToCycle.
func TestAttachTransactionsToCycle(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	userID := testUserID()
	cycleID := uuid.New()
	accountID := uuid.New()
	ids := []uuid.UUID{uuid.New(), uuid.New()}

	mock.ExpectExec("UPDATE transactions SET billing_cycle_id").
		WithArgs(cycleID, ids, userID, accountID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 2))

	err = attachTransactionsToCycle(context.Background(), mock, cycleID, accountID, ids, userID)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTransactionFingerprint(t *testing.T) {
	assert.Equal(t,
		transactionFingerprint("2024-01-15", money.FromFloat(250.5), "debit", "Coffee"),
		transactionFingerprint("2024-01-15", money.FromFloat(250.5), "debit", "  Coffee "),
	)
	assert.Equal(t,
		transactionFingerprint("2024-01-15", money.FromFloat(250.5), "debit", "Coffee"),
		transactionFingerprint("2024-01-15", money.FromFloat(250.499999), "debit", "CoFFee"),
	)
	assert.NotEqual(t,
		transactionFingerprint("2024-01-15", money.FromFloat(250.5), "debit", "Coffee"),
		transactionFingerprint("2024-01-15", money.FromFloat(250.51), "debit", "Coffee"),
	)
	assert.NotEqual(t,
		transactionFingerprint("2024-01-15", money.FromFloat(250.5), "debit", "Coffee"),
		transactionFingerprint("2024-01-16", money.FromFloat(250.5), "debit", "Coffee"),
	)
}

func TestDedupeTransactions(t *testing.T) {
	mk := func(date string, amt money.Amount, desc string) models.ImportTransaction {
		return models.ImportTransaction{Date: date, Description: desc, Amount: amt, Type: "debit"}
	}

	a := mk("2024-01-15", 100, "Coffee")
	b := mk("2024-01-15", 200, "Groceries")
	c := mk("2024-01-15", 300, "Rent")
	repeat := mk("2024-01-15", 100, "Coffee")

	t.Run("keeps all when no duplicates", func(t *testing.T) {
		kept, dupes := dedupeTransactions([]models.ImportTransaction{a, b, c}, nil)
		assert.Equal(t, []models.ImportTransaction{a, b, c}, kept)
		assert.Equal(t, 0, dupes)
	})

	t.Run("drops in-batch repeats", func(t *testing.T) {
		kept, dupes := dedupeTransactions([]models.ImportTransaction{a, b, a, c}, nil)
		assert.Equal(t, []models.ImportTransaction{a, b, c}, kept)
		assert.Equal(t, 1, dupes)
	})

	t.Run("drops rows matching existing", func(t *testing.T) {
		existing := map[string]bool{
			transactionFingerprint(a.Date, a.Amount, a.Type, a.Description): true,
		}
		kept, dupes := dedupeTransactions([]models.ImportTransaction{a, b, repeat, c}, existing)
		assert.Equal(t, []models.ImportTransaction{b, c}, kept)
		assert.Equal(t, 2, dupes)
	})
}

func newTransactionTestRouter(t *testing.T) (*gin.Engine, *Server, pgxmock.PgxPoolIface) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(mock)
	t.Cleanup(mock.Close)

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	return r, srv, mock
}

func TestGetTransactions(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.GET("/transactions", srv.GetTransactions)

	userID := testUserID()
	txnID := uuid.New()
	accountID := uuid.New()
	now := time.Now()

	// Count query.
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t WHERE t.user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))

	// Main query.
	rows := txnListRow(txnID, accountID, now, "Coffee", 250.5, "debit", nil, []string{"food"}, "", nil, "Starbucks", now, "Savings", "Food", "🍔", "#ff0000", false, nil, "", nil, "")
	mock.ExpectQuery("SELECT t.id, t.account_id, t.date").
		WithArgs(userID, 50, 0).
		WillReturnRows(rows)

	req, _ := http.NewRequest("GET", "/transactions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var res struct {
		Data  []models.Transaction `json:"data"`
		Total int                  `json:"total"`
		Page  int                  `json:"page"`
		Limit int                  `json:"limit"`
		Pages int                  `json:"pages"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Len(t, res.Data, 1)
	assert.Equal(t, "Coffee", res.Data[0].Description)
	assert.Equal(t, money.FromFloat(250.5), res.Data[0].Amount)
	assert.Equal(t, 1, res.Total)
	assert.Equal(t, 1, res.Pages)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTransactionsRejectsInvalidAccountID(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.GET("/transactions", srv.GetTransactions)

	req, _ := http.NewRequest("GET", "/transactions?accountId=not-a-uuid", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid accountId")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTransactionsRejectsMalformedFilters(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.GET("/transactions", srv.GetTransactions)

	// A malformed date and a malformed amount are rejected before any query
	// runs, rather than being passed to Postgres (a 500) or silently dropped.
	tests := []struct {
		name      string
		query     string
		errorText string
	}{
		{name: "dateFrom", query: "dateFrom=2024-1-5", errorText: "dateFrom must be YYYY-MM-DD"},
		{name: "dateTo", query: "dateTo=yesterday", errorText: "dateTo must be YYYY-MM-DD"},
		{name: "amount", query: "amount=abc", errorText: "invalid amount"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/transactions?"+tt.query, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), tt.errorText)
		})
	}
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTransactionsRejectsOutOfRangePage(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.GET("/transactions", srv.GetTransactions)

	// Rejected before any query runs, so no DB expectations are needed.
	req, _ := http.NewRequest("GET", "/transactions?page=2000000", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "page out of range")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTransactionsWithAccountSummary(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.GET("/transactions", srv.GetTransactions)

	userID := testUserID()
	txnID := uuid.New()
	accountID := uuid.New()
	now := time.Now()

	// Count query with account filter.
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t WHERE t.user_id").
		WithArgs(userID, accountID.String()).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))

	// Main query with account filter.
	rows := txnListRow(txnID, accountID, now, "Coffee", 250.5, "debit", nil, nil, "", nil, "", now, "Savings", "", "", "", false, nil, "", nil, "")
	mock.ExpectQuery("SELECT t.id, t.account_id, t.date").
		WithArgs(userID, accountID.String(), 50, 0).
		WillReturnRows(rows)

	// buildAccountSummaryRows: account lookup — no billing day, so the account
	// gets month-end "Running balance" summary rows instead of cycle rows.
	mock.ExpectQuery("SELECT a.name, a.billing_day").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"name", "billing_day"}).
			AddRow("Savings", nil))

	// computeMonthEndBalanceRows: per-month net over the full ledger (one
	// row: the current month, a single 250.5 debit).
	today := dateOnly(now)
	monthStart := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT date_trunc\\('month'").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"month", "net", "count"}).
			AddRow(monthStart, -250.5, 1))
	// The current month is still in progress: balance as of the range end.
	mock.ExpectQuery("SELECT COALESCE\\(SUM\\(").
		WithArgs(accountID, userID, dateOnly(now)).
		WillReturnRows(pgxmock.NewRows([]string{"total", "count"}).
			AddRow(-250.5, 1))

	req, _ := http.NewRequest("GET", "/transactions?accountId="+accountID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var res struct {
		Data []models.Transaction `json:"data"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	// Accounts without a billing day get a month-end "Running balance" row
	// (desc order: same date as the transaction, the row comes first).
	assert.Len(t, res.Data, 2)
	assert.True(t, res.Data[0].IsSummary)
	assert.Equal(t, "Running balance", res.Data[0].Description)
	assert.Equal(t, money.FromFloat(-250.5), res.Data[0].Amount)
	assert.Equal(t, "Savings", res.Data[0].AccountName)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTransactionsWithAccountSummaryAnyAccountType(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.GET("/transactions", srv.GetTransactions)

	userID := testUserID()
	txnID := uuid.New()
	accountID := uuid.New()
	today := dateOnly(time.Now())
	cycleID := uuid.New()

	// Count query with account filter.
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t WHERE t.user_id").
		WithArgs(userID, accountID.String()).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))

	// Main query with account filter. The transaction is attached to the cycle
	// (transactions on billing-day accounts get assigned a cycle), so it groups
	// with the summary row below.
	rows := txnListRow(txnID, accountID, today, "Groceries", 200.0, "debit", nil, nil, "", nil, "", today, "Checking", "", "", "", false, &cycleID, "This month", nil, "")
	mock.ExpectQuery("SELECT t.id, t.account_id, t.date").
		WithArgs(userID, accountID.String(), 50, 0).
		WillReturnRows(rows)

	// buildAccountSummaryRows: a bank account WITH a billing day still gets
	// summary rows (billing day presence, not account type, is the gate).
	mock.ExpectQuery("SELECT a.name, a.billing_day").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"name", "billing_day"}).
			AddRow("Checking", intPtr(5)))

	// ensureBillingCycles: no stale cycles, earliest txn, future max end date
	// (nothing to generate), then back-fill.
	mock.ExpectQuery("SELECT end_date FROM billing_cycles").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"end_date"}))
	mock.ExpectQuery("MIN\\(date\\)").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"min"}).AddRow(today.AddDate(0, 0, -30)))
	covered := pgxmock.NewRows([]string{"end_date"})
	for _, ms := range billingCycleMonths(today.AddDate(0, 0, -30), today, 5) {
		_, end := cycleDates(ms, 5)
		covered.AddRow(end)
	}
	mock.ExpectQuery("SELECT end_date FROM billing_cycles").
		WithArgs(accountID, userID).
		WillReturnRows(covered)
	mock.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM transactions WHERE account_id").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectExec("UPDATE transactions t SET billing_cycle_id").
		WithArgs(accountID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	// computeSummaryRows: one completed cycle ending today, containing the
	// transaction above.
	mock.ExpectQuery("SELECT bc.id, bc.start_date, bc.end_date, bc.label").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "start_date", "end_date", "label", "net_activity", "txn_count"}).
			AddRow(cycleID, today.AddDate(0, 0, -30), today, "This month", 500.0, 3))

	req, _ := http.NewRequest("GET", "/transactions?accountId="+accountID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var res struct {
		Data []models.Transaction `json:"data"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	// The bank account's summary row (desc sort puts it at the top of the day).
	assert.Len(t, res.Data, 2)
	assert.True(t, res.Data[0].IsSummary)
	assert.Equal(t, "Total outstanding", res.Data[0].Description)
	assert.Equal(t, money.FromFloat(500.0), res.Data[0].Amount)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTransactionsCategoryFilter(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.GET("/transactions", srv.GetTransactions)

	userID := testUserID()
	catID := uuid.New()
	txnID := uuid.New()
	accountID := uuid.New()
	now := time.Now()

	// Count query with the category filter.
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t WHERE t.user_id").
		WithArgs(userID, catID.String()).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))

	// Main query with the category filter.
	rows := txnListRow(txnID, accountID, now, "Coffee", 250.5, "debit", &catID, []string{"food"}, "", nil, "Starbucks", now, "Savings", "Food", "🍔", "#ff0000", false, nil, "", nil, "")
	mock.ExpectQuery("SELECT t.id, t.account_id, t.date").
		WithArgs(userID, catID.String(), 50, 0).
		WillReturnRows(rows)

	req, _ := http.NewRequest("GET", "/transactions?categoryId="+catID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var res struct {
		Data []models.Transaction `json:"data"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Len(t, res.Data, 1)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTransactionsPayeeNoneFilter(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.GET("/transactions", srv.GetTransactions)

	userID := testUserID()
	txnID := uuid.New()
	accountID := uuid.New()
	now := time.Now()

	// Count query with the payee:none sentinel -> payee_id IS NULL (no arg).
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t WHERE t.user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))

	rows := txnListRow(txnID, accountID, now, "Coffee", 250.5, "debit", nil, nil, "", nil, "", now, "Savings", "", "", "", false, nil, "", nil, "")
	mock.ExpectQuery("SELECT t.id, t.account_id, t.date").
		WithArgs(userID, 50, 0).
		WillReturnRows(rows)

	req, _ := http.NewRequest("GET", "/transactions?payeeId=none", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var res struct {
		Data []models.Transaction `json:"data"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Len(t, res.Data, 1)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTransactionsCategoryFilterUncategorized(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.GET("/transactions", srv.GetTransactions)

	userID := testUserID()
	txnID := uuid.New()
	accountID := uuid.New()
	now := time.Now()

	// Count query with the uncategorized sentinel -> category_id IS NULL.
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t WHERE t.user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))

	rows := txnListRow(txnID, accountID, now, "Coffee", 250.5, "debit", nil, nil, "", nil, "", now, "Savings", "", "", "", false, nil, "", nil, "")
	// Main query with the uncategorized sentinel -> no category arg, just userID + pagination.
	mock.ExpectQuery("SELECT t.id, t.account_id, t.date").
		WithArgs(userID, 50, 0).
		WillReturnRows(rows)

	req, _ := http.NewRequest("GET", "/transactions?categoryId=uncategorized", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var res struct {
		Data []models.Transaction `json:"data"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Len(t, res.Data, 1)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTransactionsCategoryFilterByType(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.GET("/transactions", srv.GetTransactions)

	userID := testUserID()
	catID := uuid.New()
	txnID := uuid.New()
	accountID := uuid.New()
	now := time.Now()

	// Count query with a group-level sentinel (category type).
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t WHERE t.user_id").
		WithArgs(userID, "expense").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))

	rows := txnListRow(txnID, accountID, now, "Coffee", 250.5, "debit", &catID, nil, "", nil, "", now, "Savings", "Food", "🍔", "#ff0000", false, nil, "", nil, "")
	mock.ExpectQuery("SELECT t.id, t.account_id, t.date").
		WithArgs(userID, "expense", 50, 0).
		WillReturnRows(rows)

	req, _ := http.NewRequest("GET", "/transactions?categoryId=expense", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var res struct {
		Data []models.Transaction `json:"data"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Len(t, res.Data, 1)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTransactionsGroupFilter(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.GET("/transactions", srv.GetTransactions)

	userID := testUserID()
	groupID := uuid.New()
	catID := uuid.New()
	txnID := uuid.New()
	accountID := uuid.New()
	now := time.Now()

	// Count query with a group filter. Unlike categoryId, groupId works with
	// UUID ids too, so custom (user-created) groups can be filtered on.
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t WHERE t.user_id").
		WithArgs(userID, groupID.String()).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))

	rows := txnListRow(txnID, accountID, now, "Coffee", 250.5, "debit", &catID, nil, "", nil, "", now, "Savings", "Food", "🍔", "#ff0000", false, nil, "", nil, "")
	mock.ExpectQuery("SELECT t.id, t.account_id, t.date").
		WithArgs(userID, groupID.String(), 50, 0).
		WillReturnRows(rows)

	req, _ := http.NewRequest("GET", "/transactions?groupId="+groupID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var res struct {
		Data []models.Transaction `json:"data"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Len(t, res.Data, 1)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestGetTransactionsCombinedFiltersShareArgs pins the QUAL-6 invariant: the
// count query and the page query receive the identical filter args, so the
// reported total always matches what the page can return.
func TestGetTransactionsCombinedFiltersShareArgs(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.GET("/transactions", srv.GetTransactions)

	userID := testUserID()
	loanID := uuid.New()

	filterArgs := []any{userID, "expense", "2024-01-01", "debit", loanID.String()}

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t WHERE t.user_id").
		WithArgs(filterArgs...).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))

	mock.ExpectQuery("SELECT t.id, t.account_id, t.date").
		WithArgs(append(append([]any{}, filterArgs...), 50, 0)...).
		WillReturnRows(pgxmock.NewRows([]string{"id"}))

	req, _ := http.NewRequest("GET",
		"/transactions?categoryId=expense&dateFrom=2024-01-01&type=debit&loanAccountId="+loanID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateTransaction(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.POST("/transactions", srv.CreateTransaction)

	userID := testUserID()
	accountID := uuid.New()
	catID := uuid.New()
	payeeID := uuid.New()
	txnID := uuid.New()

	reqBody := models.CreateTransactionRequest{
		AccountID:   accountID,
		Date:        "2024-01-15",
		Description: "Coffee",
		Amount:      money.FromFloat(250.5),
		Type:        "debit",
		CategoryID:  &catID,
		PayeeID:     &payeeID,
		Tags:        []string{"food"},
		Notes:       "morning",
	}

	mock.ExpectBegin()

	// Account ownership check.
	mock.ExpectQuery("SELECT user_id, billing_day").
		WithArgs(accountID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "billing_day", "closed", "account_type_id"}).AddRow(userID, nil, false, "bank"))

	// Insert.
	mock.ExpectQuery("INSERT INTO transactions").
		WithArgs(accountID, userID, "2024-01-15", "Coffee", money.FromFloat(250.5), "debit", &catID, &payeeID, []string{"food"}, "morning").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(txnID))

	mock.ExpectCommit()

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/transactions", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Contains(t, w.Body.String(), txnID.String())
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateTransactionAutoCategorize(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.POST("/transactions", srv.CreateTransaction)

	userID := testUserID()
	accountID := uuid.New()
	catID := uuid.New()
	txnID := uuid.New()

	reqBody := models.CreateTransactionRequest{
		AccountID:   accountID,
		Date:        "2024-01-15",
		Description: "Zomato Order #123",
		Amount:      money.FromFloat(500.0),
		Type:        "debit",
	}

	mock.ExpectBegin()

	// Account ownership check.
	mock.ExpectQuery("SELECT user_id, billing_day").
		WithArgs(accountID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "billing_day", "closed", "account_type_id"}).AddRow(userID, nil, false, "bank"))

	// Load rules for auto-categorization.
	mock.ExpectQuery("SELECT pattern, match_type, category_id, payee_id").
		WithArgs(userID).
		WillReturnRows(ruleEntryRows().
			AddRow("Zomato", "contains", catID, nil, nil, nil, nil, nil, nil, "", nil, nil, nil, nil, []string{}, ""))

	// Insert with auto-categorized category (no payee from rules).
	mock.ExpectQuery("INSERT INTO transactions").
		WithArgs(accountID, userID, "2024-01-15", "Zomato Order #123", money.FromFloat(500.0), "debit", &catID, (*uuid.UUID)(nil), ([]string)(nil), "").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(txnID))

	mock.ExpectCommit()

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/transactions", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateTransactionValidation(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.POST("/transactions", srv.CreateTransaction)

	accountID := uuid.New()
	tests := []struct {
		name string
		body string
	}{
		{name: "invalid type", body: `{"accountId":"` + accountID.String() + `","date":"2024-01-15","description":"X","amount":1,"type":"refund"}`},
		{name: "invalid amount", body: `{"accountId":"` + accountID.String() + `","date":"2024-01-15","description":"X","amount":0,"type":"debit"}`},
		{name: "invalid date", body: `{"accountId":"` + accountID.String() + `","date":"15/01/2024","description":"X","amount":1,"type":"debit"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", "/transactions", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestCreateTransactionAccountNotFound(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.POST("/transactions", srv.CreateTransaction)

	accountID := uuid.New()
	reqBody := models.CreateTransactionRequest{
		AccountID:   accountID,
		Date:        "2024-01-15",
		Description: "Coffee",
		Amount:      money.FromFloat(250.5),
		Type:        "debit",
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT user_id, billing_day").
		WithArgs(accountID).
		WillReturnError(pgx.ErrNoRows)

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/transactions", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateTransactionForbidden(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.POST("/transactions", srv.CreateTransaction)

	accountID := uuid.New()
	reqBody := models.CreateTransactionRequest{
		AccountID:   accountID,
		Date:        "2024-01-15",
		Description: "Coffee",
		Amount:      money.FromFloat(250.5),
		Type:        "debit",
	}

	// Account belongs to a different user.
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT user_id, billing_day").
		WithArgs(accountID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "billing_day", "closed", "account_type_id"}).AddRow(uuid.New(), nil, false, "bank"))

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/transactions", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkCategorize(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.POST("/transactions/bulk-categorize", srv.BulkCategorize)

	userID := testUserID()
	catID := uuid.New()
	txnIDs := []uuid.UUID{uuid.New(), uuid.New()}

	reqBody := models.BulkCategorizeRequest{
		TransactionIDs: txnIDs,
		CategoryID:     catID.String(),
	}

	mock.ExpectExec("UPDATE transactions SET category_id").
		WithArgs(catID, txnIDs, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 2))

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/transactions/bulk-categorize", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"updated":2`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkCategorizeUncategorizedSentinel(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.POST("/transactions/bulk-categorize", srv.BulkCategorize)

	userID := testUserID()
	txnIDs := []uuid.UUID{uuid.New(), uuid.New()}

	reqBody := models.BulkCategorizeRequest{
		TransactionIDs: txnIDs,
		CategoryID:     "uncategorized",
	}

	mock.ExpectExec("UPDATE transactions SET category_id = NULL").
		WithArgs(txnIDs, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 2))

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/transactions/bulk-categorize", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"updated":2`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkUpdatePayee(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.POST("/transactions/bulk-payee", srv.BulkUpdatePayee)

	userID := testUserID()
	payeeID := uuid.New()
	txnIDs := []uuid.UUID{uuid.New()}

	reqBody := models.BulkUpdatePayeeRequest{
		TransactionIDs: txnIDs,
		PayeeID:        payeeID,
	}

	mock.ExpectExec("UPDATE transactions SET payee_id").
		WithArgs(payeeID, txnIDs, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/transactions/bulk-payee", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"updated":1`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkUpdateBillingCycle(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.POST("/transactions/bulk-billing-cycle", srv.BulkUpdateBillingCycle)

	userID := testUserID()
	cycleID := uuid.New()
	txnIDs := []uuid.UUID{uuid.New(), uuid.New()}

	reqBody := models.BulkBillingCycleRequest{
		TransactionIDs: txnIDs,
		BillingCycleID: cycleID,
	}

	mock.ExpectExec("UPDATE transactions SET billing_cycle_id").
		WithArgs(cycleID, txnIDs, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 2))

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/transactions/bulk-billing-cycle", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"updated":2`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkDeleteTransactions(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.POST("/transactions/bulk-delete", srv.BulkDeleteTransactions)

	userID := testUserID()
	txnIDs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}

	reqBody := models.BulkDeleteTransactionsRequest{TransactionIDs: txnIDs}

	mock.ExpectExec("DELETE FROM transactions WHERE id = ANY").
		WithArgs(txnIDs, userID).
		WillReturnResult(pgxmock.NewResult("DELETE", 3))

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/transactions/bulk-delete", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"deleted":3`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteTransaction(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.DELETE("/transactions/:id", srv.DeleteTransaction)

	userID := testUserID()
	txnID := uuid.New()

	mock.ExpectExec("DELETE FROM transactions WHERE id").
		WithArgs(txnID, userID).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	req, _ := http.NewRequest("DELETE", "/transactions/"+txnID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteTransactionNotFound(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.DELETE("/transactions/:id", srv.DeleteTransaction)

	userID := testUserID()
	txnID := uuid.New()

	mock.ExpectExec("DELETE FROM transactions WHERE id").
		WithArgs(txnID, userID).
		WillReturnResult(pgxmock.NewResult("DELETE", 0))

	req, _ := http.NewRequest("DELETE", "/transactions/"+txnID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteTransactionInvalidID(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.DELETE("/transactions/:id", srv.DeleteTransaction)

	req, _ := http.NewRequest("DELETE", "/transactions/not-a-uuid", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAutoCategorize(t *testing.T) {
	catID := uuid.New()
	payeeID := uuid.New()
	rules := []ruleEntry{
		{Pattern: "Zomato", MatchType: "contains", CatID: catID, PayeeID: &payeeID},
	}

	matched := autoCategorize(rules, ruleContext{Description: "Zomato Order #123"})
	assert.NotNil(t, matched)
	assert.Equal(t, catID, matched.CatID)
	assert.NotNil(t, matched.PayeeID)
	assert.Equal(t, payeeID, *matched.PayeeID)

	matched = autoCategorize(rules, ruleContext{Description: "Swiggy Order"})
	assert.Nil(t, matched)

	matched = autoCategorize(nil, ruleContext{Description: "Anything"})
	assert.Nil(t, matched)
}

func TestRuleMatchesConditions(t *testing.T) {
	acct := uuid.New()
	otherAcct := uuid.New()
	catID := uuid.New()
	payeeID := uuid.New()
	linked := true
	unlinked := false
	min := money.FromFloat(100)
	max := money.FromFloat(500)
	from := "2024-01-01"
	to := "2024-12-31"

	base := ruleContext{
		Description: "Zomato Order",
		AccountID:   acct,
		Amount:      money.FromFloat(250),
		Type:        "debit",
		Date:        "2024-06-15",
		CategoryID:  &catID,
		PayeeID:     &payeeID,
		IsLinked:    true,
	}

	tests := []struct {
		name  string
		rule  ruleEntry
		ctx   ruleContext
		match bool
	}{
		{"account match", ruleEntry{Pattern: "zomato", MatchType: "contains", AccountID: &acct}, base, true},
		{"account mismatch", ruleEntry{Pattern: "zomato", MatchType: "contains", AccountID: &otherAcct}, base, false},
		{"category match", ruleEntry{Pattern: "zomato", MatchType: "contains", FilterCategoryID: &catID}, base, true},
		{"payee match", ruleEntry{Pattern: "zomato", MatchType: "contains", FilterPayeeID: &payeeID}, base, true},
		{"amount in range", ruleEntry{Pattern: "zomato", MatchType: "contains", MinAmount: &min, MaxAmount: &max}, base, true},
		{"amount below range", ruleEntry{Pattern: "zomato", MatchType: "contains", MinAmount: &max}, base, false},
		{"type match", ruleEntry{Pattern: "zomato", MatchType: "contains", TxnType: "debit"}, base, true},
		{"type mismatch", ruleEntry{Pattern: "zomato", MatchType: "contains", TxnType: "credit"}, base, false},
		{"date in window", ruleEntry{Pattern: "zomato", MatchType: "contains", DateFrom: &from, DateTo: &to}, base, true},
		{"date outside window", ruleEntry{Pattern: "zomato", MatchType: "contains", DateFrom: &to}, base, false},
		{"linked true", ruleEntry{Pattern: "zomato", MatchType: "contains", IsLinked: &linked}, base, true},
		{"linked false mismatch", ruleEntry{Pattern: "zomato", MatchType: "contains", IsLinked: &unlinked}, base, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.match, ruleMatches(tt.rule, tt.ctx))
		})
	}
}

func TestUnionTagsAndAppendNote(t *testing.T) {
	assert.Equal(t, []string{"a", "b"}, unionTags([]string{"a"}, []string{"a", "b"}))
	assert.Equal(t, []string{"a", "b"}, unionTags([]string{"a"}, []string{"", "b"}))
	assert.Equal(t, []string{"a"}, unionTags([]string{"a"}, nil))
	assert.Equal(t, "existing\nrule", appendNote("existing", "rule"))
	assert.Equal(t, "rule", appendNote("", "rule"))
	assert.Equal(t, "existing", appendNote("existing", ""))
}

func TestCreateTransactionCategoryNotOwned(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.POST("/transactions", srv.CreateTransaction)

	userID := testUserID()
	accountID := uuid.New()
	catID := uuid.New()

	reqBody := models.CreateTransactionRequest{
		AccountID:   accountID,
		Date:        "2024-01-15",
		Description: "Coffee",
		Amount:      money.FromFloat(250.5),
		Type:        "debit",
		CategoryID:  &catID,
	}

	// Account owned, but the category belongs to another user -> the
	// INSERT...SELECT matches no rows.
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT user_id, billing_day").
		WithArgs(accountID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "billing_day", "closed", "account_type_id"}).AddRow(userID, nil, false, "bank"))
	mock.ExpectQuery("INSERT INTO transactions").
		WithArgs(accountID, userID, "2024-01-15", "Coffee", money.FromFloat(250.5), "debit", &catID, (*uuid.UUID)(nil), []string(nil), "").
		WillReturnError(pgx.ErrNoRows)

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/transactions", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateTransactionPayeeNotOwned(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.POST("/transactions", srv.CreateTransaction)

	userID := testUserID()
	accountID := uuid.New()
	catID := uuid.New()
	payeeID := uuid.New()

	reqBody := models.CreateTransactionRequest{
		AccountID:   accountID,
		Date:        "2024-01-15",
		Description: "Coffee",
		Amount:      money.FromFloat(250.5),
		Type:        "debit",
		CategoryID:  &catID,
		PayeeID:     &payeeID,
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT user_id, billing_day").
		WithArgs(accountID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "billing_day", "closed", "account_type_id"}).AddRow(userID, nil, false, "bank"))
	mock.ExpectQuery("INSERT INTO transactions").
		WithArgs(accountID, userID, "2024-01-15", "Coffee", money.FromFloat(250.5), "debit", &catID, &payeeID, []string(nil), "").
		WillReturnError(pgx.ErrNoRows)

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/transactions", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateTransactionBillingCycleNotOwned(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.POST("/transactions", srv.CreateTransaction)

	userID := testUserID()
	accountID := uuid.New()
	catID := uuid.New()
	cycleID := uuid.New()

	reqBody := models.CreateTransactionRequest{
		AccountID:      accountID,
		Date:           "2024-01-15",
		Description:    "Coffee",
		Amount:         money.FromFloat(250.5),
		Type:           "debit",
		CategoryID:     &catID,
		BillingCycleID: &cycleID,
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT user_id, billing_day").
		WithArgs(accountID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "billing_day", "closed", "account_type_id"}).AddRow(userID, intPtr(5), false, "bank"))
	// Cycle belongs to another user (or another account).
	mock.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM billing_cycles").
		WithArgs(cycleID, userID, accountID).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/transactions", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateTransactionCrossUserCategory(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.PATCH("/transactions/:id", srv.UpdateTransaction)

	userID := testUserID()
	txnID := uuid.New()
	catID := uuid.New()

	// Ownership predicate fails -> 0 rows affected -> 404.
	mock.ExpectExec("UPDATE transactions SET category_id").
		WithArgs(catID, txnID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	body, _ := json.Marshal(map[string]interface{}{"categoryId": catID})
	req, _ := http.NewRequest("PATCH", "/transactions/"+txnID.String(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateTransactionCrossUserAccount(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.PATCH("/transactions/:id", srv.UpdateTransaction)

	userID := testUserID()
	txnID := uuid.New()
	otherAccountID := uuid.New()

	mock.ExpectExec("UPDATE transactions SET account_id").
		WithArgs(otherAccountID, txnID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	body, _ := json.Marshal(map[string]interface{}{"accountId": otherAccountID})
	req, _ := http.NewRequest("PATCH", "/transactions/"+txnID.String(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportTransactionsBillingCycleNotOwned(t *testing.T) {
	r, _, mock := newImportTestRouter(t)

	accountID := uuid.New()
	userID := testUserID()
	cycleID := uuid.New()

	mock.ExpectQuery("SELECT user_id, billing_day").
		WithArgs(accountID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "billing_day", "closed", "account_type_id"}).AddRow(userID, nil, false, "bank"))
	mock.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM billing_cycles").
		WithArgs(cycleID, userID, accountID).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))

	body, _ := json.Marshal(models.ImportRequest{
		AccountID:      accountID,
		BillingCycleID: &cycleID,
		Transactions:   []models.ImportTransaction{{Date: "2024-01-15", Description: "X", Amount: money.FromFloat(1), Type: "debit"}},
	})
	w := postImport(r, body)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportTransactionsPayeeNotOwned(t *testing.T) {
	r, _, mock := newImportTestRouter(t)

	accountID := uuid.New()
	userID := testUserID()
	payeeID := uuid.New()

	mock.ExpectQuery("SELECT user_id, billing_day").
		WithArgs(accountID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "billing_day", "closed", "account_type_id"}).AddRow(userID, nil, false, "bank"))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM payees").
		WithArgs([]uuid.UUID{payeeID}, userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))

	body, _ := json.Marshal(models.ImportRequest{
		AccountID: accountID,
		Transactions: []models.ImportTransaction{
			{Date: "2024-01-15", Description: "X", Amount: money.FromFloat(1), Type: "debit", PayeeID: &payeeID},
		},
	})
	w := postImport(r, body)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestComputeSummaryRows(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	userID := testUserID()
	acctID := uuid.New()

	cycleID := func(n int) uuid.UUID {
		return uuid.NewSHA1(uuid.NameSpaceOID, fmt.Appendf(nil, "cycle-%d", n))
	}

	// listBillingCycles: net activity per cycle Jan 6–Feb 5 (150, 2),
	// Feb 6–Mar 5 (200, 2), Mar 6–Apr 5 (60, 1), Apr 6–May 5 (0, 0),
	// May 6–Jun 5 (0, 0). TotalOutstanding accumulates these.
	mock.ExpectQuery("SELECT bc.id, bc.start_date, bc.end_date, bc.label").
		WithArgs(acctID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "start_date", "end_date", "label", "net_activity", "txn_count"}).
			AddRow(cycleID(1), time.Date(2024, 1, 6, 0, 0, 0, 0, time.UTC), time.Date(2024, 2, 5, 0, 0, 0, 0, time.UTC), "Feb 2024", 150.0, 2).
			AddRow(cycleID(2), time.Date(2024, 2, 6, 0, 0, 0, 0, time.UTC), time.Date(2024, 3, 5, 0, 0, 0, 0, time.UTC), "Mar 2024", 200.0, 2).
			AddRow(cycleID(3), time.Date(2024, 3, 6, 0, 0, 0, 0, time.UTC), time.Date(2024, 4, 5, 0, 0, 0, 0, time.UTC), "Apr 2024", 60.0, 1).
			AddRow(cycleID(4), time.Date(2024, 4, 6, 0, 0, 0, 0, time.UTC), time.Date(2024, 5, 5, 0, 0, 0, 0, time.UTC), "May 2024", 0.0, 0).
			AddRow(cycleID(5), time.Date(2024, 5, 6, 0, 0, 0, 0, time.UTC), time.Date(2024, 6, 5, 0, 0, 0, 0, time.UTC), "Jun 2024", 0.0, 0))

	// Current in-progress cycle (Mar 6–Apr 5 contains Mar 31): running balance
	// of the whole account up to Mar 31 (150 + 200 + 60).
	mock.ExpectQuery("SELECT COALESCE\\(SUM\\(CASE WHEN t.type = 'debit'").
		WithArgs(acctID, userID, time.Date(2024, 3, 31, 0, 0, 0, 0, time.UTC)).
		WillReturnRows(pgxmock.NewRows([]string{"total", "count"}).AddRow(410.0, 3))

	rows := srv.computeSummaryRows(c, userID, acctID, "Amex", "2024-01-01", "2024-03-31")

	// Feb 5 (running balance 150), Mar 5 (running balance 150+200=350), and a
	// current-cycle row at Mar 31 (running balance 350+60=410).
	assert.Len(t, rows, 3)
	assert.Equal(t, "Total outstanding", rows[0].Description)
	assert.Equal(t, time.Date(2024, 2, 5, 0, 0, 0, 0, time.UTC), dateOnly(rows[0].Date))
	assert.Equal(t, money.FromFloat(150.0), rows[0].Amount)
	assert.Equal(t, time.Date(2024, 3, 5, 0, 0, 0, 0, time.UTC), dateOnly(rows[1].Date))
	assert.Equal(t, money.FromFloat(350.0), rows[1].Amount)
	assert.Equal(t, time.Date(2024, 3, 31, 0, 0, 0, 0, time.UTC), dateOnly(rows[2].Date))
	assert.Equal(t, money.FromFloat(410.0), rows[2].Amount)
	assert.True(t, rows[0].IsSummary)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestComputeSummaryRowsFirstOfMonth(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

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
		WillReturnRows(pgxmock.NewRows([]string{"id", "start_date", "end_date", "label", "net_activity", "txn_count"}).
			AddRow(cycleID(1), time.Date(2023, 12, 2, 0, 0, 0, 0, time.UTC), time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), "Jan 2024", 0.0, 0).
			AddRow(cycleID(2), time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC), "Feb 2024", 100.0, 1).
			AddRow(cycleID(3), time.Date(2024, 2, 2, 0, 0, 0, 0, time.UTC), time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC), "Mar 2024", 0.0, 0).
			AddRow(cycleID(4), time.Date(2024, 3, 2, 0, 0, 0, 0, time.UTC), time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC), "Apr 2024", 0.0, 0))

	// Current in-progress cycle (Mar 2–Apr 1 contains Mar 31): running balance
	// of the whole account up to Mar 31 (only the 100 debit so far).
	mock.ExpectQuery("SELECT COALESCE\\(SUM\\(CASE WHEN t.type = 'debit'").
		WithArgs(acctID, userID, time.Date(2024, 3, 31, 0, 0, 0, 0, time.UTC)).
		WillReturnRows(pgxmock.NewRows([]string{"total", "count"}).AddRow(100.0, 1))

	rows := srv.computeSummaryRows(c, userID, acctID, "Amex", "2024-01-01", "2024-03-31")

	var found money.Amount
	for _, r := range rows {
		if r.Description == "Total outstanding" && dateOnly(r.Date).Equal(time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)) {
			found = r.Amount
		}
	}
	assert.Equal(t, money.FromFloat(100.0), found)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMergeSummaryRows(t *testing.T) {
	cycleA := uuid.New()
	cycleB := uuid.New()
	mkTxn := func(day int, cycle *uuid.UUID) models.Transaction {
		return models.Transaction{ID: uuid.New(), Date: time.Date(2024, 1, day, 0, 0, 0, 0, time.UTC), BillingCycleID: cycle}
	}
	mkRow := func(day int, cycle uuid.UUID) models.Transaction {
		return models.Transaction{ID: uuid.New(), Date: time.Date(2024, 1, day, 0, 0, 0, 0, time.UTC), IsSummary: true, Description: "Total outstanding", BillingCycleID: &cycle}
	}
	days := func(txns []models.Transaction) []int {
		got := make([]int, len(txns))
		for i, m := range txns {
			got[i] = m.Date.Day()
		}
		return got
	}

	t.Run("desc groups each row with its cycle's transactions", func(t *testing.T) {
		// Cycle A ends day 10, cycle B ends day 6. A future-dated transaction
		// with no cycle (day 12) must still be preserved, not dropped.
		txns := []models.Transaction{
			mkTxn(12, nil),
			mkTxn(10, &cycleA), mkTxn(9, &cycleA),
			mkTxn(6, &cycleB), mkTxn(5, &cycleB),
		}
		rows := []models.Transaction{mkRow(10, cycleA), mkRow(6, cycleB)}

		merged := mergeSummaryRows(txns, rows, "date", "DESC")
		assert.Equal(t, []int{12, 10, 10, 9, 6, 6, 5}, days(merged))
		// Each row leads its own cycle's transactions in descending order.
		assert.True(t, merged[1].IsSummary)
		assert.Equal(t, "Total outstanding", merged[1].Description)
		assert.True(t, merged[4].IsSummary)
	})

	t.Run("asc groups each row with its cycle's transactions", func(t *testing.T) {
		txns := []models.Transaction{
			mkTxn(5, &cycleB), mkTxn(6, &cycleB),
			mkTxn(9, &cycleA), mkTxn(10, &cycleA),
			mkTxn(12, nil),
		}
		rows := []models.Transaction{mkRow(6, cycleB), mkRow(10, cycleA)}

		merged := mergeSummaryRows(txns, rows, "date", "ASC")
		assert.Equal(t, []int{5, 6, 6, 9, 10, 10, 12}, days(merged))
		// Each row trails its own cycle's transactions in ascending order.
		assert.True(t, merged[2].IsSummary)
		assert.True(t, merged[5].IsSummary)
	})

	t.Run("transactions without a cycle are never dropped", func(t *testing.T) {
		txns := []models.Transaction{
			mkTxn(12, nil),
			mkTxn(10, &cycleA), mkTxn(9, &cycleA),
			mkTxn(6, &cycleB), mkTxn(5, &cycleB),
		}
		rows := []models.Transaction{mkRow(10, cycleA), mkRow(6, cycleB)}

		merged := mergeSummaryRows(txns, rows, "date", "DESC")
		assert.Len(t, merged, len(txns)+len(rows))
	})
}

func TestMergeSummaryRowsHiddenOnNonDateSort(t *testing.T) {
	mkTxn := func(day int) models.Transaction {
		return models.Transaction{ID: uuid.New(), Date: time.Date(2024, 1, day, 0, 0, 0, 0, time.UTC)}
	}
	txns := []models.Transaction{mkTxn(10), mkTxn(8), mkTxn(5)}
	summary := []models.Transaction{mkTxn(9), mkTxn(6)}

	for _, sortBy := range []string{"amount", "description"} {
		merged := mergeSummaryRows(txns, summary, sortBy, "DESC")
		assert.Len(t, merged, len(txns), "summary rows must be hidden when sorting by %s", sortBy)
	}
}

func TestCreateTransactionWithGlobalCategory(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.POST("/transactions", srv.CreateTransaction)

	userID := testUserID()
	accountID := uuid.New()
	globalCatID := uuid.New()
	txnID := uuid.New()

	reqBody := models.CreateTransactionRequest{
		AccountID:   accountID,
		Date:        "2024-01-15",
		Description: "Coffee",
		Amount:      money.FromFloat(250.5),
		Type:        "debit",
		CategoryID:  &globalCatID,
	}

	mock.ExpectBegin()

	mock.ExpectQuery("SELECT user_id, billing_day").
		WithArgs(accountID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "billing_day", "closed", "account_type_id"}).AddRow(userID, nil, false, "bank"))

	// The ownership guard must admit global categories (user_id IS NULL) —
	// the matcher pins the exact predicate so a future revert to a
	// user-only check fails this test.
	mock.ExpectQuery(regexp.QuoteMeta("WHERE ($7::uuid IS NULL OR EXISTS (SELECT 1 FROM categories c WHERE c.id = $7 AND (c.user_id = $2 OR c.user_id IS NULL)))")).
		WithArgs(accountID, userID, "2024-01-15", "Coffee", money.FromFloat(250.5), "debit", &globalCatID, (*uuid.UUID)(nil), []string(nil), "").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(txnID))

	mock.ExpectCommit()

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/transactions", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateTransactionInvalidDate(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.PATCH("/transactions/:id", srv.UpdateTransaction)

	txnID := uuid.New()
	body, _ := json.Marshal(map[string]interface{}{"date": "15-01-2024"})
	req, _ := http.NewRequest("PATCH", "/transactions/"+txnID.String(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid date")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateTransactionNonPositiveAmount(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.PATCH("/transactions/:id", srv.UpdateTransaction)

	txnID := uuid.New()
	for _, amount := range []float64{0, -100} {
		t.Run(fmt.Sprintf("amount=%v", amount), func(t *testing.T) {
			body, _ := json.Marshal(map[string]interface{}{"amount": amount})
			req, _ := http.NewRequest("PATCH", "/transactions/"+txnID.String(), bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), "amount must be positive")
		})
	}
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkCategorizeTooManyIDs(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.POST("/transactions/bulk-categorize", srv.BulkCategorize)

	ids := make([]uuid.UUID, maxBulkBatch+1)
	for i := range ids {
		ids[i] = uuid.New()
	}
	body, _ := json.Marshal(map[string]interface{}{"transactionIds": ids, "categoryId": uuid.New().String()})
	req, _ := http.NewRequest("POST", "/transactions/bulk-categorize", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "too many transaction ids")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTransactionsSearchEscapesWildcards(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.GET("/transactions", srv.GetTransactions)

	userID := testUserID()
	// The same escaped pattern is bound once per searched field (description,
	// notes, payee name, tags).
	pattern := `%100\%%`

	// Searching "100%" must pass the escaped pattern (%100\%%), not the raw
	// text that LIKE would interpret as a wildcard.
	// The handler issues the COUNT query first, then the paged list.
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions").
		WithArgs(userID, pattern, pattern, pattern, pattern).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT t.id, t.account_id").
		WithArgs(userID, pattern, pattern, pattern, pattern, 50, 0).
		WillReturnRows(pgxmock.NewRows([]string{"id", "account_id", "date", "description", "amount", "type", "category_id", "tags", "notes", "payee_id", "payee", "created_at", "account_name", "category_name", "category_icon", "category_color", "is_linked", "billing_cycle_id", "billing_cycle_label"}))

	req, _ := http.NewRequest("GET", "/transactions?search=100%25", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTransactionsSearchSpansNotesPayeeAndTags(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.GET("/transactions", srv.GetTransactions)

	userID := testUserID()
	pattern := "%coffee%"

	// The free-text term must reach every text field the list renders, not just
	// the description. Payee and tags are correlated subqueries so the count
	// query (which has no joins) stays valid.
	searchClause := `SELECT COUNT\(\*\) FROM transactions t WHERE t\.user_id = \$1 AND ` +
		`\(LOWER\(t\.description\) LIKE LOWER\(\$2\)` +
		`[\s\S]*LOWER\(COALESCE\(t\.notes, ''\)\) LIKE LOWER\(\$3\)` +
		`[\s\S]*FROM payees sp WHERE sp\.id = t\.payee_id AND LOWER\(sp\.name\) LIKE LOWER\(\$4\)` +
		`[\s\S]*FROM unnest\(t\.tags\) AS tag WHERE LOWER\(tag\) LIKE LOWER\(\$5\)`
	mock.ExpectQuery(searchClause).
		WithArgs(userID, pattern, pattern, pattern, pattern).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT t.id, t.account_id").
		WithArgs(userID, pattern, pattern, pattern, pattern, 50, 0).
		WillReturnRows(pgxmock.NewRows(txnListCols))

	req, _ := http.NewRequest("GET", "/transactions?search=coffee", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTransactionsRecurringLinkage(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.GET("/transactions", srv.GetTransactions)

	userID := testUserID()
	txnID, accountID, seriesID := uuid.New(), uuid.New(), uuid.New()
	now := time.Now()

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t WHERE t.user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT t.id, t.account_id, t.date").
		WithArgs(userID, 50, 0).
		WillReturnRows(pgxmock.NewRows(txnListCols).
			AddRow(txnID, accountID, now, "Netflix", 15.99, "debit", nil, []string{}, "", nil, "Netflix", now, "Checking", "", "", "", false, nil, "", nil, "", &seriesID, "Netflix"))

	req, _ := http.NewRequest("GET", "/transactions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var res struct {
		Data []models.Transaction `json:"data"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Len(t, res.Data, 1)
	assert.NotNil(t, res.Data[0].RecurringSeriesID)
	assert.Equal(t, seriesID, *res.Data[0].RecurringSeriesID)
	assert.Equal(t, "Netflix", res.Data[0].RecurringSeriesName)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTransactionsRecurringFilter(t *testing.T) {
	for _, filter := range []string{"linked", "unlinked"} {
		t.Run(filter, func(t *testing.T) {
			r, srv, mock := newTransactionTestRouter(t)
			r.GET("/transactions", srv.GetTransactions)
			userID := testUserID()

			mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t WHERE t.user_id").
				WithArgs(userID).
				WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
			mock.ExpectQuery("SELECT t.id, t.account_id, t.date").
				WithArgs(userID, 50, 0).
				WillReturnRows(pgxmock.NewRows(txnListCols))

			req, _ := http.NewRequest("GET", "/transactions?recurring="+filter, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
