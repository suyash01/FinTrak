package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fintrak/backend/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v3"
	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// BulkLinkLoan (POST /transactions/bulk-loan)
// ---------------------------------------------------------------------------

// loanTransactionRows is the shared mock for the two bulk pre-checks that
// return counts (ownership + on-loan-account) and the already-attached check.
const loanCountCols = "count"

func TestBulkLinkLoanAttach(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions/bulk-loan", BulkLinkLoan)

	userID := testUserID()
	loanID := uuid.New()
	payeeID := uuid.New()
	txn1, txn2 := uuid.New(), uuid.New()
	ids := []uuid.UUID{txn1, txn2}

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t WHERE t.id = ANY").
		WithArgs(ids, userID).
		WillReturnRows(pgxmock.NewRows([]string{loanCountCols}).AddRow(2))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t").
		WithArgs(ids, userID).
		WillReturnRows(pgxmock.NewRows([]string{loanCountCols}).AddRow(0))
	mock.ExpectQuery("SELECT user_id, account_type_id FROM accounts").
		WithArgs(loanID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "account_type_id"}).AddRow(userID, "loan"))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM loan_attachments").
		WithArgs(ids, userID).
		WillReturnRows(pgxmock.NewRows([]string{loanCountCols}).AddRow(0))
	// The write is transactional: insert attachments, sync payees, commit.
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO loan_attachments").
		WithArgs(loanID, ids, userID).
		WillReturnResult(pgxmock.NewResult("INSERT", 2))
	mock.ExpectQuery("SELECT id FROM payees WHERE account_id").
		WithArgs(loanID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(payeeID))
	mock.ExpectExec("UPDATE transactions SET payee_id").
		WithArgs(payeeID, ids, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 2))
	mock.ExpectCommit()

	body, _ := json.Marshal(models.BulkLoanRequest{TransactionIDs: ids, LoanAccountID: &loanID})
	req, _ := http.NewRequest("POST", "/transactions/bulk-loan", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var res struct {
		Attached int64 `json:"attached"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Equal(t, int64(2), res.Attached)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkLinkLoanAttachWithoutPayee(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions/bulk-loan", BulkLinkLoan)

	userID := testUserID()
	loanID := uuid.New()
	txn1 := uuid.New()
	ids := []uuid.UUID{txn1}

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t WHERE t.id = ANY").
		WithArgs(ids, userID).
		WillReturnRows(pgxmock.NewRows([]string{loanCountCols}).AddRow(1))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t").
		WithArgs(ids, userID).
		WillReturnRows(pgxmock.NewRows([]string{loanCountCols}).AddRow(0))
	mock.ExpectQuery("SELECT user_id, account_type_id FROM accounts").
		WithArgs(loanID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "account_type_id"}).AddRow(userID, "loan"))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM loan_attachments").
		WithArgs(ids, userID).
		WillReturnRows(pgxmock.NewRows([]string{loanCountCols}).AddRow(0))
	// A loan whose linked payee was deleted still attaches; payees are
	// left untouched (no UPDATE).
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO loan_attachments").
		WithArgs(loanID, ids, userID).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectQuery("SELECT id FROM payees WHERE account_id").
		WithArgs(loanID, userID).
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectCommit()

	body, _ := json.Marshal(models.BulkLoanRequest{TransactionIDs: ids, LoanAccountID: &loanID})
	req, _ := http.NewRequest("POST", "/transactions/bulk-loan", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkLinkLoanDetach(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions/bulk-loan", BulkLinkLoan)

	userID := testUserID()
	txn1, txn2 := uuid.New(), uuid.New()
	ids := []uuid.UUID{txn1, txn2}

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t WHERE t.id = ANY").
		WithArgs(ids, userID).
		WillReturnRows(pgxmock.NewRows([]string{loanCountCols}).AddRow(2))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t").
		WithArgs(ids, userID).
		WillReturnRows(pgxmock.NewRows([]string{loanCountCols}).AddRow(0))
	mock.ExpectExec("DELETE FROM loan_attachments").
		WithArgs(ids, userID).
		WillReturnResult(pgxmock.NewResult("DELETE", 2))

	// loanAccountId omitted -> detach.
	body, _ := json.Marshal(models.BulkLoanRequest{TransactionIDs: ids})
	req, _ := http.NewRequest("POST", "/transactions/bulk-loan", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var res struct {
		Detached int64 `json:"detached"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Equal(t, int64(2), res.Detached)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkLinkLoanNoTransactionIDs(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions/bulk-loan", BulkLinkLoan)

	req, _ := http.NewRequest("POST", "/transactions/bulk-loan", bytes.NewBufferString(`{"transactionIds":[]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "no transaction ids")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkLinkLoanTooManyIDs(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions/bulk-loan", BulkLinkLoan)

	ids := make([]uuid.UUID, maxBulkBatch+1)
	for i := range ids {
		ids[i] = uuid.New()
	}
	loanID := uuid.New()
	body, _ := json.Marshal(models.BulkLoanRequest{TransactionIDs: ids, LoanAccountID: &loanID})
	req, _ := http.NewRequest("POST", "/transactions/bulk-loan", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "too many transaction ids")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkLinkLoanTransactionNotFound(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions/bulk-loan", BulkLinkLoan)

	userID := testUserID()
	txn1 := uuid.New()
	ids := []uuid.UUID{txn1}

	// Only 0 of the 1 requested exist -> rejected before any attach query.
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t WHERE t.id = ANY").
		WithArgs(ids, userID).
		WillReturnRows(pgxmock.NewRows([]string{loanCountCols}).AddRow(0))

	body, _ := json.Marshal(models.BulkLoanRequest{TransactionIDs: ids})
	req, _ := http.NewRequest("POST", "/transactions/bulk-loan", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "not found")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkLinkLoanTransactionOnLoanAccount(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions/bulk-loan", BulkLinkLoan)

	userID := testUserID()
	txn1 := uuid.New()
	ids := []uuid.UUID{txn1}

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t WHERE t.id = ANY").
		WithArgs(ids, userID).
		WillReturnRows(pgxmock.NewRows([]string{loanCountCols}).AddRow(1))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t").
		WithArgs(ids, userID).
		WillReturnRows(pgxmock.NewRows([]string{loanCountCols}).AddRow(1))

	body, _ := json.Marshal(models.BulkLoanRequest{TransactionIDs: ids})
	req, _ := http.NewRequest("POST", "/transactions/bulk-loan", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "loan account and cannot be attached")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkLinkLoanLoanAccountNotFound(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions/bulk-loan", BulkLinkLoan)

	userID := testUserID()
	loanID := uuid.New()
	txn1 := uuid.New()
	ids := []uuid.UUID{txn1}

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t WHERE t.id = ANY").
		WithArgs(ids, userID).
		WillReturnRows(pgxmock.NewRows([]string{loanCountCols}).AddRow(1))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t").
		WithArgs(ids, userID).
		WillReturnRows(pgxmock.NewRows([]string{loanCountCols}).AddRow(0))
	mock.ExpectQuery("SELECT user_id, account_type_id FROM accounts").
		WithArgs(loanID).
		WillReturnError(pgx.ErrNoRows)

	body, _ := json.Marshal(models.BulkLoanRequest{TransactionIDs: ids, LoanAccountID: &loanID})
	req, _ := http.NewRequest("POST", "/transactions/bulk-loan", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "loan account not found")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkLinkLoanLoanAccountForbidden(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions/bulk-loan", BulkLinkLoan)

	userID := testUserID()
	loanID := uuid.New()
	txn1 := uuid.New()
	ids := []uuid.UUID{txn1}

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t WHERE t.id = ANY").
		WithArgs(ids, userID).
		WillReturnRows(pgxmock.NewRows([]string{loanCountCols}).AddRow(1))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t").
		WithArgs(ids, userID).
		WillReturnRows(pgxmock.NewRows([]string{loanCountCols}).AddRow(0))
	mock.ExpectQuery("SELECT user_id, account_type_id FROM accounts").
		WithArgs(loanID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "account_type_id"}).AddRow(uuid.New(), "loan"))

	body, _ := json.Marshal(models.BulkLoanRequest{TransactionIDs: ids, LoanAccountID: &loanID})
	req, _ := http.NewRequest("POST", "/transactions/bulk-loan", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkLinkLoanTargetNotLoanType(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions/bulk-loan", BulkLinkLoan)

	userID := testUserID()
	bankID := uuid.New()
	txn1 := uuid.New()
	ids := []uuid.UUID{txn1}

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t WHERE t.id = ANY").
		WithArgs(ids, userID).
		WillReturnRows(pgxmock.NewRows([]string{loanCountCols}).AddRow(1))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t").
		WithArgs(ids, userID).
		WillReturnRows(pgxmock.NewRows([]string{loanCountCols}).AddRow(0))
	mock.ExpectQuery("SELECT user_id, account_type_id FROM accounts").
		WithArgs(bankID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "account_type_id"}).AddRow(userID, "bank"))

	body, _ := json.Marshal(models.BulkLoanRequest{TransactionIDs: ids, LoanAccountID: &bankID})
	req, _ := http.NewRequest("POST", "/transactions/bulk-loan", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Loan / EMI")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkLinkLoanAlreadyAttached(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions/bulk-loan", BulkLinkLoan)

	userID := testUserID()
	loanID := uuid.New()
	txn1 := uuid.New()
	ids := []uuid.UUID{txn1}

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t WHERE t.id = ANY").
		WithArgs(ids, userID).
		WillReturnRows(pgxmock.NewRows([]string{loanCountCols}).AddRow(1))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t").
		WithArgs(ids, userID).
		WillReturnRows(pgxmock.NewRows([]string{loanCountCols}).AddRow(0))
	mock.ExpectQuery("SELECT user_id, account_type_id FROM accounts").
		WithArgs(loanID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "account_type_id"}).AddRow(userID, "loan"))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM loan_attachments").
		WithArgs(ids, userID).
		WillReturnRows(pgxmock.NewRows([]string{loanCountCols}).AddRow(1))

	body, _ := json.Marshal(models.BulkLoanRequest{TransactionIDs: ids, LoanAccountID: &loanID})
	req, _ := http.NewRequest("POST", "/transactions/bulk-loan", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "already linked")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkLinkLoanInsertUniqueViolationRace(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions/bulk-loan", BulkLinkLoan)

	userID := testUserID()
	loanID := uuid.New()
	txn1 := uuid.New()
	ids := []uuid.UUID{txn1}

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t WHERE t.id = ANY").
		WithArgs(ids, userID).
		WillReturnRows(pgxmock.NewRows([]string{loanCountCols}).AddRow(1))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t").
		WithArgs(ids, userID).
		WillReturnRows(pgxmock.NewRows([]string{loanCountCols}).AddRow(0))
	mock.ExpectQuery("SELECT user_id, account_type_id FROM accounts").
		WithArgs(loanID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "account_type_id"}).AddRow(userID, "loan"))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM loan_attachments").
		WithArgs(ids, userID).
		WillReturnRows(pgxmock.NewRows([]string{loanCountCols}).AddRow(0))
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO loan_attachments").
		WithArgs(loanID, ids, userID).
		WillReturnError(&pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint \"loan_attachments_transaction_id_key\""})
	mock.ExpectRollback()

	body, _ := json.Marshal(models.BulkLoanRequest{TransactionIDs: ids, LoanAccountID: &loanID})
	req, _ := http.NewRequest("POST", "/transactions/bulk-loan", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "already linked")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// GetTransactions loan filters
// ---------------------------------------------------------------------------

func TestGetTransactionsLoanAccountIdFilter(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.GET("/transactions", GetTransactions)

	userID := testUserID()
	txnID := uuid.New()
	accountID := uuid.New()
	loanID := uuid.New()
	now := time.Now()

	// Count with the loanAccountId filter appended.
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t WHERE t.user_id").
		WithArgs(userID, loanID.String()).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))

	rows := pgxmock.NewRows([]string{"id", "account_id", "date", "description", "amount", "type", "category_id", "tags", "notes", "payee_id", "payee", "created_at", "account_name", "category_name", "category_icon", "category_color", "is_linked", "billing_cycle_id", "billing_cycle_label", "loan_account_id", "loan_account_name"}).
		AddRow(txnID, accountID, now, "EMI", 15000.0, "debit", nil, nil, "", nil, "", now, "Savings", "", "", "", false, nil, "", &loanID, "Home Loan")
	mock.ExpectQuery("SELECT t.id, t.account_id, t.date").
		WithArgs(userID, loanID.String(), 50, 0).
		WillReturnRows(rows)

	req, _ := http.NewRequest("GET", "/transactions?loanAccountId="+loanID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var res struct {
		Data []models.Transaction `json:"data"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Len(t, res.Data, 1)
	assert.NotNil(t, res.Data[0].LoanAccountID)
	assert.Equal(t, loanID, *res.Data[0].LoanAccountID)
	assert.Equal(t, "Home Loan", res.Data[0].LoanAccountName)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTransactionsExcludeAttached(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.GET("/transactions", GetTransactions)

	userID := testUserID()
	txnID := uuid.New()
	accountID := uuid.New()
	now := time.Now()

	// excludeAttached adds no args (pure IS NULL predicate).
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t WHERE t.user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))

	rows := pgxmock.NewRows([]string{"id", "account_id", "date", "description", "amount", "type", "category_id", "tags", "notes", "payee_id", "payee", "created_at", "account_name", "category_name", "category_icon", "category_color", "is_linked", "billing_cycle_id", "billing_cycle_label", "loan_account_id", "loan_account_name"}).
		AddRow(txnID, accountID, now, "Salary", 50000.0, "credit", nil, nil, "", nil, "", now, "Savings", "", "", "", false, nil, "", nil, "")
	mock.ExpectQuery("SELECT t.id, t.account_id, t.date").
		WithArgs(userID, 50, 0).
		WillReturnRows(rows)

	req, _ := http.NewRequest("GET", "/transactions?excludeAttached=true", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// Closed accounts: transaction mutations become no-ops / rejections
// ---------------------------------------------------------------------------

func TestCreateTransactionClosedAccount(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions", CreateTransaction)

	userID := testUserID()
	accountID := uuid.New()

	// Account exists, is owned, but is closed -> 409 before any insert.
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT user_id, billing_day").
		WithArgs(accountID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "billing_day", "closed", "account_type_id"}).AddRow(userID, nil, true, "bank"))

	reqBody := models.CreateTransactionRequest{
		AccountID:   accountID,
		Date:        "2024-01-15",
		Description: "Coffee",
		Amount:      250.5,
		Type:        "debit",
	}
	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/transactions", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "account is closed")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateTransactionLoanAccountRejected(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions", CreateTransaction)

	userID := testUserID()
	accountID := uuid.New()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT user_id, billing_day").
		WithArgs(accountID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "billing_day", "closed", "account_type_id"}).AddRow(userID, nil, false, "loan"))

	reqBody := models.CreateTransactionRequest{
		AccountID:   accountID,
		Date:        "2024-01-15",
		Description: "EMI",
		Amount:      15000,
		Type:        "debit",
	}
	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/transactions", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "loan accounts cannot have transactions")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportTransactionsClosedAccount(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.POST("/transactions/import", ImportTransactions)

	userID := testUserID()
	accountID := uuid.New()

	mock.ExpectQuery("SELECT user_id, billing_day").
		WithArgs(accountID).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "billing_day", "closed", "account_type_id"}).AddRow(userID, nil, true, "bank"))

	body, _ := json.Marshal(models.ImportRequest{
		AccountID: accountID,
		Transactions: []models.ImportTransaction{
			{Date: "2024-01-15", Description: "Coffee", Amount: 250.5, Type: "debit"},
		},
	})
	req, _ := http.NewRequest("POST", "/transactions/import", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "account is closed")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateTransactionOnClosedAccountNoop(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.PATCH("/transactions/:id", UpdateTransaction)

	txnID := uuid.New()
	catID := uuid.New()
	userID := testUserID()

	// The WHERE now excludes transactions on closed accounts -> 0 rows -> 404.
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

func TestUpdateTransactionMoveToClosedAccountNoop(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.PATCH("/transactions/:id", UpdateTransaction)

	txnID := uuid.New()
	newAccountID := uuid.New()
	userID := testUserID()

	// The account EXISTS predicate now requires NOT closed -> 0 rows -> 404.
	mock.ExpectExec("UPDATE transactions SET account_id").
		WithArgs(newAccountID, txnID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	body, _ := json.Marshal(map[string]interface{}{"accountId": newAccountID})
	req, _ := http.NewRequest("PATCH", "/transactions/"+txnID.String(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteTransactionOnClosedAccountNoop(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.DELETE("/transactions/:id", DeleteTransaction)

	txnID := uuid.New()
	userID := testUserID()

	mock.ExpectExec("DELETE FROM transactions").
		WithArgs(txnID, userID).
		WillReturnResult(pgxmock.NewResult("DELETE", 0))

	req, _ := http.NewRequest("DELETE", "/transactions/"+txnID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "not found")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// Accounts: closed flag + loan balance SQL
// ---------------------------------------------------------------------------

func TestGetAccountsCarriesClosedAndLoanBalanceSQL(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.GET("/accounts", GetAccounts)

	userID := testUserID()
	createdAt := time.Now()

	rows := pgxmock.NewRows([]string{"id", "name", "account_type_id", "account_type_name", "bank", "currency", "color", "is_default", "billing_day", "created_at", "closed", "balance"}).
		AddRow(uuid.New(), "Home Loan", "loan", "Loan / EMI", "HDFC", "INR", "#22c55e", false, nil, createdAt, false, 450000.0)

	// The matcher pins the loan balance branch (attachments sum) so a future
	// revert to the plain transaction-sum would fail this test.
	mock.ExpectQuery("WHEN at.id = 'loan'").
		WithArgs(userID, userID).
		WillReturnRows(rows)

	req, _ := http.NewRequest("GET", "/accounts", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var accounts []models.Account
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &accounts))
	assert.Len(t, accounts, 1)
	assert.Equal(t, "Home Loan", accounts[0].Name)
	assert.Equal(t, 450000.0, accounts[0].Balance)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateAccountSetsClosed(t *testing.T) {
	r, mock := newTransactionTestRouter(t)
	r.PUT("/accounts/:id", UpdateAccount)

	accountID := uuid.New()
	userID := testUserID()

	mock.ExpectBegin()
	// closed:true -> the SQL carries ", closed = $10" with true.
	mock.ExpectQuery("WITH updated AS").
		WithArgs("Savings", "bank", "Axis", "INR", "#06b6d4", (*bool)(nil), accountID, userID, true).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "account_type_id", "account_type_name", "bank", "currency", "color", "is_default", "billing_day", "created_at", "closed", "balance"}).
			AddRow(accountID, "Savings", "bank", "Bank Account", "Axis", "INR", "#06b6d4", false, nil, time.Now(), true, 0.0))
	mock.ExpectExec("UPDATE payees SET name").
		WithArgs("Savings", accountID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	req, _ := http.NewRequest("PUT", "/accounts/"+accountID.String(),
		bytes.NewBufferString(`{"name":"Savings","accountTypeId":"bank","bank":"Axis","currency":"INR","color":"#06b6d4","closed":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var account models.Account
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &account))
	assert.True(t, account.Closed)
	assert.NoError(t, mock.ExpectationsWereMet())
}
