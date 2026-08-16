package handlers

import (
	"bytes"
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
	mock.ExpectQuery("SELECT user_id FROM accounts WHERE id = \\$1").
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
	mock.ExpectQuery("SELECT user_id FROM accounts WHERE id = \\$1").
		WithArgs(accountID).
		WillReturnRows(mock.NewRows([]string{"user_id"}).AddRow(uuid.New()))

	body, _ := json.Marshal(models.ImportRequest{
		AccountID:    accountID,
		Transactions: []models.ImportTransaction{{Date: "2024-01-15", Description: "X", Amount: 1, Type: "debit"}},
	})
	w := postImport(r, body)

	assert.Equal(t, http.StatusForbidden, w.Code)
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
	rows := pgxmock.NewRows([]string{"id", "account_id", "date", "description", "amount", "type", "category_id", "tags", "notes", "payee_id", "payee", "created_at", "account_name", "category_name", "category_icon", "category_color", "is_linked", "link_count", "link_id"}).
		AddRow(txnID, accountID, now, "Coffee", 250.5, "debit", nil, []string{"food"}, "", nil, "Starbucks", now, "Savings", "Food", "🍔", "#ff0000", false, 0, nil)
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
	rows := pgxmock.NewRows([]string{"id", "account_id", "date", "description", "amount", "type", "category_id", "tags", "notes", "payee_id", "payee", "created_at", "account_name", "category_name", "category_icon", "category_color", "is_linked", "link_count", "link_id"}).
		AddRow(txnID, accountID, now, "Coffee", 250.5, "debit", nil, nil, "", nil, "", now, "Savings", "", "", "", false, 0, nil)
	mock.ExpectQuery("SELECT t.id, t.account_id, t.date").
		WithArgs(userID, accountID.String(), 50, 0).
		WillReturnRows(rows)

	// buildAccountSummaryRows: account lookup.
	mock.ExpectQuery("SELECT a.name, a.account_type_id, at.positive_txn_type").
		WithArgs(accountID.String(), userID).
		WillReturnRows(pgxmock.NewRows([]string{"name", "account_type_id", "positive_txn_type", "billing_day"}).
			AddRow("Savings", "bank", "credit", 0))

	// buildAccountSummaryRows: transactions for running balance.
	mock.ExpectQuery("SELECT date, amount, type FROM transactions WHERE account_id").
		WithArgs(accountID.String(), userID).
		WillReturnRows(pgxmock.NewRows([]string{"date", "amount", "type"}).
			AddRow(now, 1000.0, "credit"))

	req, _ := http.NewRequest("GET", "/transactions?accountId="+accountID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var res struct {
		Data []models.Transaction `json:"data"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	// The summary "Balance" row should be merged into the response.
	assert.Len(t, res.Data, 2)

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

	// Account ownership check.
	mock.ExpectQuery("SELECT user_id FROM accounts WHERE id").
		WithArgs(accountID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id"}).AddRow(userID))

	// Insert.
	mock.ExpectQuery("INSERT INTO transactions").
		WithArgs(accountID, userID, "2024-01-15", "Coffee", 250.5, "debit", &catID, &payeeID, []string{"food"}, "morning").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(txnID))

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

	// Account ownership check.
	mock.ExpectQuery("SELECT user_id FROM accounts WHERE id").
		WithArgs(accountID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id"}).AddRow(userID))

	// Load rules for auto-categorization.
	mock.ExpectQuery("SELECT pattern, match_type, category_id, payee_id FROM rules").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"pattern", "match_type", "category_id", "payee_id"}).
			AddRow("Zomato", "contains", catID, nil))

	// Insert with auto-categorized category (no payee from rules).
	mock.ExpectQuery("INSERT INTO transactions").
		WithArgs(accountID, userID, "2024-01-15", "Zomato Order #123", 500.0, "debit", &catID, (*uuid.UUID)(nil), []string(nil), "").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(txnID))

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

	mock.ExpectQuery("SELECT user_id FROM accounts WHERE id").
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
	mock.ExpectQuery("SELECT user_id FROM accounts WHERE id").
		WithArgs(accountID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id"}).AddRow(uuid.New()))

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
		CategoryID:     catID,
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
