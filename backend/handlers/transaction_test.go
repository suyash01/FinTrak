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

	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v3"
	"github.com/stretchr/testify/assert"
)

func TestUpdateTransactionClearsCategory(t *testing.T) {
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
	r.PATCH("/transactions/:id", UpdateTransaction)

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

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.PATCH("/transactions/:id", UpdateTransaction)

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

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.PATCH("/transactions/:id", UpdateTransaction)

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

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.PATCH("/transactions/:id", UpdateTransaction)

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

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.PATCH("/transactions/:id", UpdateTransaction)

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

func TestCreateTransactionCreditCardAutoAssign(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions", CreateTransaction)

	userID := testUserID()
	accountID := uuid.New()
	catID := uuid.New()
	txnID := uuid.New()

	reqBody := models.CreateTransactionRequest{
		AccountID:   accountID,
		Date:        "2024-01-15",
		Description: "Coffee",
		Amount:      250.5,
		Type:        "debit",
		CategoryID:  &catID,
	}

	mock.ExpectBegin()

	// Account ownership check (credit card).
	mock.ExpectQuery("SELECT user_id, billing_day").
		WithArgs(accountID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "billing_day"}).AddRow(userID, intPtr(5)))

	// Insert.
	mock.ExpectQuery("INSERT INTO transactions").
		WithArgs(accountID, userID, "2024-01-15", "Coffee", 250.5, "debit", &catID, (*uuid.UUID)(nil), []string(nil), "").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(txnID))

	// ensureBillingCycles: alignment check (no stale cycles).
	mock.ExpectQuery("SELECT end_date FROM billing_cycles").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"end_date"}))

	// ensureBillingCycles: earliest transaction.
	mock.ExpectQuery("SELECT MIN\\(date\\) FROM transactions").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"min"}).AddRow(time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)))

	// ensureBillingCycles: latest cycle end in the future -> nothing to generate.
	mock.ExpectQuery("SELECT MAX\\(end_date\\) FROM billing_cycles").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"max"}).AddRow(dateOnly(time.Now()).AddDate(0, 1, 0)))

	// ensureBillingCycles: back-fill (attaches this transaction by date).
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
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions", CreateTransaction)

	userID := testUserID()
	accountID := uuid.New()
	catID := uuid.New()
	cycleID := uuid.New()
	txnID := uuid.New()

	reqBody := models.CreateTransactionRequest{
		AccountID:      accountID,
		Date:           "2024-01-15",
		Description:    "Coffee",
		Amount:         250.5,
		Type:           "debit",
		CategoryID:     &catID,
		BillingCycleID: &cycleID,
	}

	mock.ExpectBegin()

	// Account ownership check (credit card).
	mock.ExpectQuery("SELECT user_id, billing_day").
		WithArgs(accountID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "billing_day"}).AddRow(userID, intPtr(5)))

	// Explicit billing cycle ownership check.
	mock.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM billing_cycles").
		WithArgs(cycleID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))

	// Insert.
	mock.ExpectQuery("INSERT INTO transactions").
		WithArgs(accountID, userID, "2024-01-15", "Coffee", 250.5, "debit", &catID, (*uuid.UUID)(nil), []string(nil), "").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(txnID))

	// ensureBillingCycles: alignment check (no stale cycles).
	mock.ExpectQuery("SELECT end_date FROM billing_cycles").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"end_date"}))

	// ensureBillingCycles: earliest transaction.
	mock.ExpectQuery("SELECT MIN\\(date\\) FROM transactions").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"min"}).AddRow(time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)))

	// ensureBillingCycles: latest cycle end in the future -> nothing to generate.
	mock.ExpectQuery("SELECT MAX\\(end_date\\) FROM billing_cycles").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"max"}).AddRow(dateOnly(time.Now()).AddDate(0, 1, 0)))

	// ensureBillingCycles: back-fill.
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

func newImportTestRouter(t *testing.T) (*gin.Engine, pgxmock.PgxPoolIface) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	oldPool := db.Pool
	db.Pool = mock
	t.Cleanup(func() {
		db.Pool = oldPool
		mock.Close()
	})

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.POST("/transactions/import", ImportTransactions)
	return r, mock
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
		Amount:      250.5,
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
		{name: "invalid type", request: models.ImportRequest{AccountID: accountID, Transactions: []models.ImportTransaction{{Date: "2024-01-15", Description: "X", Amount: 1, Type: "refund"}}}},
		{name: "invalid amount", request: models.ImportRequest{AccountID: accountID, Transactions: []models.ImportTransaction{{Date: "2024-01-15", Description: "X", Amount: 0, Type: "debit"}}}},
		{name: "invalid date", request: models.ImportRequest{AccountID: accountID, Transactions: []models.ImportTransaction{{Date: "15/01/2024", Description: "X", Amount: 1, Type: "debit"}}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, mock := newImportTestRouter(t)

			body, _ := json.Marshal(tt.request)
			w := postImport(r, body)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestImportTransactionsAccountNotFound(t *testing.T) {
	r, mock := newImportTestRouter(t)

	accountID := uuid.New()
	mock.ExpectQuery("SELECT user_id, billing_day").
		WithArgs(accountID).
		WillReturnError(pgx.ErrNoRows)

	body, _ := json.Marshal(models.ImportRequest{
		AccountID:    accountID,
		Transactions: []models.ImportTransaction{{Date: "2024-01-15", Description: "X", Amount: 1, Type: "debit"}},
	})
	w := postImport(r, body)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportTransactionsAccountForbidden(t *testing.T) {
	r, mock := newImportTestRouter(t)

	accountID := uuid.New()
	mock.ExpectQuery("SELECT user_id, billing_day").
		WithArgs(accountID).
		WillReturnRows(mock.NewRows([]string{"user_id", "billing_day"}).AddRow(uuid.New(), nil))

	body, _ := json.Marshal(models.ImportRequest{
		AccountID:    accountID,
		Transactions: []models.ImportTransaction{{Date: "2024-01-15", Description: "X", Amount: 1, Type: "debit"}},
	})
	w := postImport(r, body)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func newValidateTestRouter(t *testing.T) (*gin.Engine, pgxmock.PgxPoolIface) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	oldPool := db.Pool
	db.Pool = mock
	t.Cleanup(func() {
		db.Pool = oldPool
		mock.Close()
	})

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.POST("/transactions/validate", ValidateTransactions)
	return r, mock
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
		Amount:      250.5,
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
		{name: "invalid type", request: models.ValidateTransactionsRequest{AccountID: accountID, Transactions: []models.ImportTransaction{{Date: "2024-01-15", Description: "X", Amount: 1, Type: "refund"}}}},
		{name: "invalid amount", request: models.ValidateTransactionsRequest{AccountID: accountID, Transactions: []models.ImportTransaction{{Date: "2024-01-15", Description: "X", Amount: 0, Type: "debit"}}}},
		{name: "invalid date", request: models.ValidateTransactionsRequest{AccountID: accountID, Transactions: []models.ImportTransaction{{Date: "15/01/2024", Description: "X", Amount: 1, Type: "debit"}}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, mock := newValidateTestRouter(t)

			body, _ := json.Marshal(tt.request)
			w := postValidate(r, body)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestValidateTransactionsAccountNotFound(t *testing.T) {
	r, mock := newValidateTestRouter(t)

	accountID := uuid.New()
	mock.ExpectQuery("SELECT user_id FROM accounts").
		WithArgs(accountID).
		WillReturnError(pgx.ErrNoRows)

	body, _ := json.Marshal(models.ValidateTransactionsRequest{
		AccountID:    accountID,
		Transactions: []models.ImportTransaction{{Date: "2024-01-15", Description: "X", Amount: 1, Type: "debit"}},
	})
	w := postValidate(r, body)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestValidateTransactionsAccountForbidden(t *testing.T) {
	r, mock := newValidateTestRouter(t)

	accountID := uuid.New()
	mock.ExpectQuery("SELECT user_id FROM accounts").
		WithArgs(accountID).
		WillReturnRows(mock.NewRows([]string{"user_id"}).AddRow(uuid.New()))

	body, _ := json.Marshal(models.ValidateTransactionsRequest{
		AccountID:    accountID,
		Transactions: []models.ImportTransaction{{Date: "2024-01-15", Description: "X", Amount: 1, Type: "debit"}},
	})
	w := postValidate(r, body)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestValidateTransactionsSuccess(t *testing.T) {
	r, mock := newValidateTestRouter(t)

	accountID := uuid.New()
	userID := testUserID()

	mock.ExpectQuery("SELECT user_id FROM accounts").
		WithArgs(accountID).
		WillReturnRows(mock.NewRows([]string{"user_id"}).AddRow(userID))

	// One existing transaction that matches the first candidate, and one that
	// does not match anything.
	existingDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT date, amount, type, description FROM transactions").
		WithArgs(accountID, userID).
		WillReturnRows(mock.NewRows([]string{"date", "amount", "type", "description"}).
			AddRow(existingDate, 250.5, "debit", "Coffee").
			AddRow(existingDate, 99.0, "credit", "Cashback"))

	body, _ := json.Marshal(models.ValidateTransactionsRequest{
		AccountID: accountID,
		Transactions: []models.ImportTransaction{
			{Date: "2024-01-15", Description: "Coffee", Amount: 250.5, Type: "debit"},    // exists
			{Date: "2024-01-16", Description: "Groceries", Amount: 120.0, Type: "debit"}, // new
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
	ids := []uuid.UUID{uuid.New(), uuid.New()}

	mock.ExpectExec("UPDATE transactions SET billing_cycle_id").
		WithArgs(cycleID, ids, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 2))

	err = attachTransactionsToCycle(context.Background(), mock, cycleID, ids, userID)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTransactionFingerprint(t *testing.T) {
	assert.Equal(t,
		transactionFingerprint("2024-01-15", 250.5, "debit", "Coffee"),
		transactionFingerprint("2024-01-15", 250.5, "debit", "  Coffee "),
	)
	assert.Equal(t,
		transactionFingerprint("2024-01-15", 250.5, "debit", "Coffee"),
		transactionFingerprint("2024-01-15", 250.499999, "debit", "CoFFee"),
	)
	assert.NotEqual(t,
		transactionFingerprint("2024-01-15", 250.5, "debit", "Coffee"),
		transactionFingerprint("2024-01-15", 250.51, "debit", "Coffee"),
	)
	assert.NotEqual(t,
		transactionFingerprint("2024-01-15", 250.5, "debit", "Coffee"),
		transactionFingerprint("2024-01-16", 250.5, "debit", "Coffee"),
	)
}

func TestDedupeTransactions(t *testing.T) {
	mk := func(date string, amt float64, desc string) models.ImportTransaction {
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

func newTransactionTestRouter(t *testing.T) (*gin.Engine, pgxmock.PgxPoolIface) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	oldPool := db.Pool
	db.Pool = mock
	t.Cleanup(func() {
		db.Pool = oldPool
		mock.Close()
	})

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	return r, mock
}

func TestGetTransactions(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.GET("/transactions", GetTransactions)

	userID := testUserID()
	txnID := uuid.New()
	accountID := uuid.New()
	now := time.Now()

	// Count query.
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t WHERE t.user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))

	// Main query.
	rows := pgxmock.NewRows([]string{"id", "account_id", "date", "description", "amount", "type", "category_id", "tags", "notes", "payee_id", "payee", "created_at", "account_name", "category_name", "category_icon", "category_color", "is_linked", "billing_cycle_id", "billing_cycle_label"}).
		AddRow(txnID, accountID, now, "Coffee", 250.5, "debit", nil, []string{"food"}, "", nil, "Starbucks", now, "Savings", "Food", "🍔", "#ff0000", false, nil, "")
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
	assert.Equal(t, 250.5, res.Data[0].Amount)
	assert.Equal(t, 1, res.Total)
	assert.Equal(t, 1, res.Pages)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTransactionsWithAccountSummary(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.GET("/transactions", GetTransactions)

	userID := testUserID()
	txnID := uuid.New()
	accountID := uuid.New()
	now := time.Now()

	// Count query with account filter.
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t WHERE t.user_id").
		WithArgs(userID, accountID.String()).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))

	// Main query with account filter.
	rows := pgxmock.NewRows([]string{"id", "account_id", "date", "description", "amount", "type", "category_id", "tags", "notes", "payee_id", "payee", "created_at", "account_name", "category_name", "category_icon", "category_color", "is_linked", "billing_cycle_id", "billing_cycle_label"}).
		AddRow(txnID, accountID, now, "Coffee", 250.5, "debit", nil, nil, "", nil, "", now, "Savings", "", "", "", false, nil, "")
	mock.ExpectQuery("SELECT t.id, t.account_id, t.date").
		WithArgs(userID, accountID.String(), 50, 0).
		WillReturnRows(rows)

	// buildAccountSummaryRows: account lookup (no billing day -> no summary rows).
	mock.ExpectQuery("SELECT a.name, a.billing_day").
		WithArgs(accountID.String(), userID).
		WillReturnRows(pgxmock.NewRows([]string{"name", "billing_day"}).
			AddRow("Savings", nil))

	req, _ := http.NewRequest("GET", "/transactions?accountId="+accountID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var res struct {
		Data []models.Transaction `json:"data"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	// Accounts without a billing day get no synthetic summary rows.
	assert.Len(t, res.Data, 1)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTransactionsWithAccountSummaryAnyAccountType(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.GET("/transactions", GetTransactions)

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
	rows := pgxmock.NewRows([]string{"id", "account_id", "date", "description", "amount", "type", "category_id", "tags", "notes", "payee_id", "payee", "created_at", "account_name", "category_name", "category_icon", "category_color", "is_linked", "billing_cycle_id", "billing_cycle_label"}).
		AddRow(txnID, accountID, today, "Groceries", 200.0, "debit", nil, nil, "", nil, "", today, "Checking", "", "", "", false, &cycleID, "This month")
	mock.ExpectQuery("SELECT t.id, t.account_id, t.date").
		WithArgs(userID, accountID.String(), 50, 0).
		WillReturnRows(rows)

	// buildAccountSummaryRows: a bank account WITH a billing day still gets
	// summary rows (billing day presence, not account type, is the gate).
	mock.ExpectQuery("SELECT a.name, a.billing_day").
		WithArgs(accountID.String(), userID).
		WillReturnRows(pgxmock.NewRows([]string{"name", "billing_day"}).
			AddRow("Checking", intPtr(5)))

	// ensureBillingCycles: no stale cycles, earliest txn, future max end date
	// (nothing to generate), then back-fill.
	mock.ExpectQuery("SELECT end_date FROM billing_cycles").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"end_date"}))
	mock.ExpectQuery("SELECT MIN\\(date\\) FROM transactions").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"min"}).AddRow(today.AddDate(0, 0, -30)))
	mock.ExpectQuery("SELECT MAX\\(end_date\\) FROM billing_cycles").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"max"}).AddRow(today.AddDate(0, 1, 0)))
	mock.ExpectExec("UPDATE transactions t SET billing_cycle_id").
		WithArgs(accountID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	// computeSummaryRows: one completed cycle ending today, containing the
	// transaction above.
	mock.ExpectQuery("SELECT bc.id, bc.start_date, bc.end_date, bc.label").
		WithArgs(accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "start_date", "end_date", "label", "total_outstanding", "txn_count"}).
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
	assert.Equal(t, 500.0, res.Data[0].Amount)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTransactionsCategoryFilter(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.GET("/transactions", GetTransactions)

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
	rows := pgxmock.NewRows([]string{"id", "account_id", "date", "description", "amount", "type", "category_id", "tags", "notes", "payee_id", "payee", "created_at", "account_name", "category_name", "category_icon", "category_color", "is_linked", "billing_cycle_id", "billing_cycle_label"}).
		AddRow(txnID, accountID, now, "Coffee", 250.5, "debit", &catID, []string{"food"}, "", nil, "Starbucks", now, "Savings", "Food", "🍔", "#ff0000", false, nil, "")
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

func TestGetTransactionsCategoryFilterUncategorized(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.GET("/transactions", GetTransactions)

	userID := testUserID()
	txnID := uuid.New()
	accountID := uuid.New()
	now := time.Now()

	// Count query with the uncategorized sentinel -> category_id IS NULL.
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t WHERE t.user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))

	rows := pgxmock.NewRows([]string{"id", "account_id", "date", "description", "amount", "type", "category_id", "tags", "notes", "payee_id", "payee", "created_at", "account_name", "category_name", "category_icon", "category_color", "is_linked", "billing_cycle_id", "billing_cycle_label"}).
		AddRow(txnID, accountID, now, "Coffee", 250.5, "debit", nil, nil, "", nil, "", now, "Savings", "", "", "", false, nil, "")
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
	r, mock := newTransactionTestRouter(t)
	r.GET("/transactions", GetTransactions)

	userID := testUserID()
	catID := uuid.New()
	txnID := uuid.New()
	accountID := uuid.New()
	now := time.Now()

	// Count query with a group-level sentinel (category type).
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t WHERE t.user_id").
		WithArgs(userID, "expense").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))

	rows := pgxmock.NewRows([]string{"id", "account_id", "date", "description", "amount", "type", "category_id", "tags", "notes", "payee_id", "payee", "created_at", "account_name", "category_name", "category_icon", "category_color", "is_linked", "billing_cycle_id", "billing_cycle_label"}).
		AddRow(txnID, accountID, now, "Coffee", 250.5, "debit", &catID, nil, "", nil, "", now, "Savings", "Food", "🍔", "#ff0000", false, nil, "")
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
	r, mock := newTransactionTestRouter(t)
	r.GET("/transactions", GetTransactions)

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

	rows := pgxmock.NewRows([]string{"id", "account_id", "date", "description", "amount", "type", "category_id", "tags", "notes", "payee_id", "payee", "created_at", "account_name", "category_name", "category_icon", "category_color", "is_linked", "billing_cycle_id", "billing_cycle_label"}).
		AddRow(txnID, accountID, now, "Coffee", 250.5, "debit", &catID, nil, "", nil, "", now, "Savings", "Food", "🍔", "#ff0000", false, nil, "")
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

func TestCreateTransaction(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions", CreateTransaction)

	userID := testUserID()
	accountID := uuid.New()
	catID := uuid.New()
	payeeID := uuid.New()
	txnID := uuid.New()

	reqBody := models.CreateTransactionRequest{
		AccountID:   accountID,
		Date:        "2024-01-15",
		Description: "Coffee",
		Amount:      250.5,
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
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "billing_day"}).AddRow(userID, nil))

	// Insert.
	mock.ExpectQuery("INSERT INTO transactions").
		WithArgs(accountID, userID, "2024-01-15", "Coffee", 250.5, "debit", &catID, &payeeID, []string{"food"}, "morning").
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
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions", CreateTransaction)

	userID := testUserID()
	accountID := uuid.New()
	catID := uuid.New()
	txnID := uuid.New()

	reqBody := models.CreateTransactionRequest{
		AccountID:   accountID,
		Date:        "2024-01-15",
		Description: "Zomato Order #123",
		Amount:      500.0,
		Type:        "debit",
	}

	mock.ExpectBegin()

	// Account ownership check.
	mock.ExpectQuery("SELECT user_id, billing_day").
		WithArgs(accountID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "billing_day"}).AddRow(userID, nil))

	// Load rules for auto-categorization.
	mock.ExpectQuery("SELECT pattern, match_type, category_id, payee_id FROM rules").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"pattern", "match_type", "category_id", "payee_id"}).
			AddRow("Zomato", "contains", catID, nil))

	// Insert with auto-categorized category (no payee from rules).
	mock.ExpectQuery("INSERT INTO transactions").
		WithArgs(accountID, userID, "2024-01-15", "Zomato Order #123", 500.0, "debit", &catID, (*uuid.UUID)(nil), []string(nil), "").
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
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions", CreateTransaction)

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
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions", CreateTransaction)

	accountID := uuid.New()
	reqBody := models.CreateTransactionRequest{
		AccountID:   accountID,
		Date:        "2024-01-15",
		Description: "Coffee",
		Amount:      250.5,
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
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions", CreateTransaction)

	accountID := uuid.New()
	reqBody := models.CreateTransactionRequest{
		AccountID:   accountID,
		Date:        "2024-01-15",
		Description: "Coffee",
		Amount:      250.5,
		Type:        "debit",
	}

	// Account belongs to a different user.
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT user_id, billing_day").
		WithArgs(accountID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "billing_day"}).AddRow(uuid.New(), nil))

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/transactions", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkCategorize(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions/bulk-categorize", BulkCategorize)

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
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions/bulk-categorize", BulkCategorize)

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
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions/bulk-payee", BulkUpdatePayee)

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
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions/bulk-billing-cycle", BulkUpdateBillingCycle)

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
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions/bulk-delete", BulkDeleteTransactions)

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
	r, mock := newTransactionTestRouter(t)
	r.DELETE("/transactions/:id", DeleteTransaction)

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
	r, mock := newTransactionTestRouter(t)
	r.DELETE("/transactions/:id", DeleteTransaction)

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
	r, mock := newTransactionTestRouter(t)
	r.DELETE("/transactions/:id", DeleteTransaction)

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

	cat, payee := autoCategorize(rules, "Zomato Order #123")
	assert.NotNil(t, cat)
	assert.Equal(t, catID, *cat)
	assert.NotNil(t, payee)
	assert.Equal(t, payeeID, *payee)

	cat, payee = autoCategorize(rules, "Swiggy Order")
	assert.Nil(t, cat)
	assert.Nil(t, payee)

	cat, payee = autoCategorize(nil, "Anything")
	assert.Nil(t, cat)
	assert.Nil(t, payee)
}

func TestCreateTransactionCategoryNotOwned(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions", CreateTransaction)

	userID := testUserID()
	accountID := uuid.New()
	catID := uuid.New()

	reqBody := models.CreateTransactionRequest{
		AccountID:   accountID,
		Date:        "2024-01-15",
		Description: "Coffee",
		Amount:      250.5,
		Type:        "debit",
		CategoryID:  &catID,
	}

	// Account owned, but the category belongs to another user -> the
	// INSERT...SELECT matches no rows.
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT user_id, billing_day").
		WithArgs(accountID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "billing_day"}).AddRow(userID, nil))
	mock.ExpectQuery("INSERT INTO transactions").
		WithArgs(accountID, userID, "2024-01-15", "Coffee", 250.5, "debit", &catID, (*uuid.UUID)(nil), []string(nil), "").
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
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions", CreateTransaction)

	userID := testUserID()
	accountID := uuid.New()
	catID := uuid.New()
	payeeID := uuid.New()

	reqBody := models.CreateTransactionRequest{
		AccountID:   accountID,
		Date:        "2024-01-15",
		Description: "Coffee",
		Amount:      250.5,
		Type:        "debit",
		CategoryID:  &catID,
		PayeeID:     &payeeID,
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT user_id, billing_day").
		WithArgs(accountID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "billing_day"}).AddRow(userID, nil))
	mock.ExpectQuery("INSERT INTO transactions").
		WithArgs(accountID, userID, "2024-01-15", "Coffee", 250.5, "debit", &catID, &payeeID, []string(nil), "").
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
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions", CreateTransaction)

	userID := testUserID()
	accountID := uuid.New()
	catID := uuid.New()
	cycleID := uuid.New()

	reqBody := models.CreateTransactionRequest{
		AccountID:      accountID,
		Date:           "2024-01-15",
		Description:    "Coffee",
		Amount:         250.5,
		Type:           "debit",
		CategoryID:     &catID,
		BillingCycleID: &cycleID,
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT user_id, billing_day").
		WithArgs(accountID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "billing_day"}).AddRow(userID, intPtr(5)))
	// Cycle belongs to another user.
	mock.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM billing_cycles").
		WithArgs(cycleID, userID).
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
	r, mock := newTransactionTestRouter(t)
	r.PATCH("/transactions/:id", UpdateTransaction)

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
	r, mock := newTransactionTestRouter(t)
	r.PATCH("/transactions/:id", UpdateTransaction)

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
	r, mock := newImportTestRouter(t)

	accountID := uuid.New()
	userID := testUserID()
	cycleID := uuid.New()

	mock.ExpectQuery("SELECT user_id, billing_day").
		WithArgs(accountID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "billing_day"}).AddRow(userID, nil))
	mock.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM billing_cycles").
		WithArgs(cycleID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))

	body, _ := json.Marshal(models.ImportRequest{
		AccountID:      accountID,
		BillingCycleID: &cycleID,
		Transactions:   []models.ImportTransaction{{Date: "2024-01-15", Description: "X", Amount: 1, Type: "debit"}},
	})
	w := postImport(r, body)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportTransactionsPayeeNotOwned(t *testing.T) {
	r, mock := newImportTestRouter(t)

	accountID := uuid.New()
	userID := testUserID()
	payeeID := uuid.New()

	mock.ExpectQuery("SELECT user_id, billing_day").
		WithArgs(accountID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "billing_day"}).AddRow(userID, nil))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM payees").
		WithArgs([]uuid.UUID{payeeID}, userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))

	body, _ := json.Marshal(models.ImportRequest{
		AccountID: accountID,
		Transactions: []models.ImportTransaction{
			{Date: "2024-01-15", Description: "X", Amount: 1, Type: "debit", PayeeID: &payeeID},
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

	rows := computeSummaryRows(c, userID, acctID, "Amex", "2024-01-01", "2024-03-31")

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

func TestComputeSummaryRowsFirstOfMonth(t *testing.T) {
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

	rows := computeSummaryRows(c, userID, acctID, "Amex", "2024-01-01", "2024-03-31")

	var found float64
	for _, r := range rows {
		if r.Description == "Total outstanding" && dateOnly(r.Date).Equal(time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)) {
			found = r.Amount
		}
	}
	assert.Equal(t, 100.0, found)

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
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions", CreateTransaction)

	userID := testUserID()
	accountID := uuid.New()
	globalCatID := uuid.New()
	txnID := uuid.New()

	reqBody := models.CreateTransactionRequest{
		AccountID:   accountID,
		Date:        "2024-01-15",
		Description: "Coffee",
		Amount:      250.5,
		Type:        "debit",
		CategoryID:  &globalCatID,
	}

	mock.ExpectBegin()

	mock.ExpectQuery("SELECT user_id, billing_day").
		WithArgs(accountID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "billing_day"}).AddRow(userID, nil))

	// The ownership guard must admit global categories (user_id IS NULL) —
	// the matcher pins the exact predicate so a future revert to a
	// user-only check fails this test.
	mock.ExpectQuery(regexp.QuoteMeta("WHERE ($7::uuid IS NULL OR EXISTS (SELECT 1 FROM categories c WHERE c.id = $7 AND (c.user_id = $2 OR c.user_id IS NULL)))")).
		WithArgs(accountID, userID, "2024-01-15", "Coffee", 250.5, "debit", &globalCatID, (*uuid.UUID)(nil), []string(nil), "").
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
	r, mock := newTransactionTestRouter(t)
	r.PATCH("/transactions/:id", UpdateTransaction)

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
	r, mock := newTransactionTestRouter(t)
	r.PATCH("/transactions/:id", UpdateTransaction)

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
