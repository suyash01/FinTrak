package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
