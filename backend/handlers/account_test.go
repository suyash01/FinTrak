package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetAccounts(t *testing.T) {
	// Initialize mock
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	// Switch global pool to mock
	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	// Setup Gin
	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.GET("/accounts", GetAccounts)

	// Define expected data
	userID := testUserID()
	createdAt := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	rows := pgxmock.NewRows([]string{"id", "name", "account_type_id", "account_type_name", "bank", "currency", "color", "is_default", "billing_day", "created_at", "balance"}).
		AddRow(uuid.New(), "Savings", "bank", "Bank Account", "HDFC", "INR", "#000000", true, intPtr(1), createdAt, 1000.50).
		AddRow(uuid.New(), "Credit Card", "credit_card", "Credit Card", "SBI", "INR", "#ff0000", false, intPtr(5), createdAt, 500.00)

	mock.ExpectQuery("SELECT a.id, a.name, a.account_type_id, at.name as account_type_name, a.bank, a.currency, a.color").
		WithArgs(userID, userID).
		WillReturnRows(rows)

	// Perform request
	req, _ := http.NewRequest("GET", "/accounts", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Assertions
	assert.Equal(t, http.StatusOK, w.Code)

	var accounts []models.Account
	err = json.Unmarshal(w.Body.Bytes(), &accounts)
	assert.NoError(t, err)
	assert.Len(t, accounts, 2)
	assert.Equal(t, "Savings", accounts[0].Name)
	assert.Equal(t, 1000.50, accounts[0].Balance)
	assert.Equal(t, createdAt, accounts[0].CreatedAt)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateAccount(t *testing.T) {
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
	r.POST("/accounts", CreateAccount)

	accountID := uuid.New()
	userID := testUserID()
	reqBody := models.CreateAccountRequest{
		Name:          "New Account",
		AccountTypeID: "bank",
		Bank:          "Axis",
	}

	// Expect Transaction
	mock.ExpectBegin()

	// Expect Insert Account (billing day omitted -> NULL).
	mock.ExpectQuery("INSERT INTO accounts").
		WithArgs(userID, reqBody.Name, reqBody.AccountTypeID, reqBody.Bank, "INR", "#06b6d4", false, (*int)(nil)).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "account_type_id", "bank", "currency", "color", "is_default", "billing_day", "created_at"}).
			AddRow(accountID, reqBody.Name, reqBody.AccountTypeID, reqBody.Bank, "INR", "#06b6d4", false, intPtr(1), time.Now()))

	// Expect Account Type Name Fetch
	mock.ExpectQuery("SELECT name FROM account_types").
		WithArgs(reqBody.AccountTypeID).
		WillReturnRows(pgxmock.NewRows([]string{"name"}).AddRow("Bank Account"))

	// Expect Insert/Update Payee
	mock.ExpectExec("INSERT INTO payees").
		WithArgs(userID, reqBody.Name, accountID).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	mock.ExpectCommit()

	// Perform request
	jsonBody, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/accounts", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Assertions
	assert.Equal(t, http.StatusCreated, w.Code)

	var account models.Account
	err = json.Unmarshal(w.Body.Bytes(), &account)
	assert.NoError(t, err)
	assert.Equal(t, accountID, account.ID)
	assert.Equal(t, "New Account", account.Name)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateAccount(t *testing.T) {
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
	r.PUT("/accounts/:id", UpdateAccount)

	accountID := uuid.New()
	userID := testUserID()
	isDefault := true
	reqBody := models.UpdateAccountRequest{
		Name:          "Updated Account",
		AccountTypeID: "bank",
		Bank:          "Axis",
		Currency:      "INR",
		Color:         "#06b6d4",
		IsDefault:     &isDefault,
	}

	// Expect Transaction
	mock.ExpectBegin()

	// Expect clearing other defaults for the user
	mock.ExpectExec("UPDATE accounts SET is_default = FALSE WHERE user_id = \\$1 AND id <> \\$2").
		WithArgs(userID, accountID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	// Expect Update Account (billing_day omitted from the request, so the SQL
	// must NOT contain a billing_day clause and only 8 args are sent)
	mock.ExpectQuery("WITH updated AS").
		WithArgs(reqBody.Name, reqBody.AccountTypeID, reqBody.Bank, reqBody.Currency, reqBody.Color, &isDefault, accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "account_type_id", "account_type_name", "bank", "currency", "color", "is_default", "billing_day", "created_at", "balance"}).
			AddRow(accountID, reqBody.Name, reqBody.AccountTypeID, "Bank Account", reqBody.Bank, reqBody.Currency, reqBody.Color, true, intPtr(1), time.Now(), 0.0))

	// Expect Update Payee
	mock.ExpectExec("UPDATE payees SET name = \\$1 WHERE account_id = \\$2").
		WithArgs(reqBody.Name, accountID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	mock.ExpectCommit()

	// Request body is a raw JSON string: the OptionalInt billingDay field has
	// no MarshalJSON, so struct marshaling would emit "billingDay":{} and fail
	// binding.
	body := fmt.Sprintf(`{"name":%q,"accountTypeId":%q,"bank":%q,"currency":%q,"color":%q,"isDefault":true}`,
		reqBody.Name, reqBody.AccountTypeID, reqBody.Bank, reqBody.Currency, reqBody.Color)
	req, _ := http.NewRequest("PUT", "/accounts/"+accountID.String(), bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Assertions
	assert.Equal(t, http.StatusOK, w.Code)

	var account models.Account
	err = json.Unmarshal(w.Body.Bytes(), &account)
	assert.NoError(t, err)
	assert.Equal(t, accountID, account.ID)
	assert.Equal(t, "Updated Account", account.Name)
	assert.True(t, account.IsDefault)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func testUserID() uuid.UUID {
	return uuid.MustParse("00000000-0000-0000-0000-000000000001")
}

// intPtr returns a pointer to n. pgxmock's Scan copies values into the
// destination when they are assignable, so pointer-typed struct fields
// (e.g. Account.BillingDay *int) need pointer mock values.
func intPtr(n int) *int {
	return &n
}

func testAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("userID", testUserID())
		c.Next()
	}
}

func newAccountTestRouter(t *testing.T) (*gin.Engine, pgxmock.PgxPoolIface) {
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

func TestDeleteAccount(t *testing.T) {
	r, mock := newAccountTestRouter(t)
	r.DELETE("/accounts/:id", DeleteAccount)

	userID := testUserID()
	accountID := uuid.New()
	const txns = 3

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM transactions WHERE account_id").
		WithArgs(accountID, userID).
		WillReturnResult(pgxmock.NewResult("DELETE", txns))
	mock.ExpectExec("DELETE FROM payees WHERE account_id").
		WithArgs(accountID, userID).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec("DELETE FROM accounts WHERE id").
		WithArgs(accountID, userID).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectCommit()

	req, _ := http.NewRequest("DELETE", "/accounts/"+accountID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Message             string `json:"message"`
		TransactionsDeleted int64  `json:"transactionsDeleted"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "deleted", resp.Message)
	assert.Equal(t, int64(txns), resp.TransactionsDeleted)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteAccountNotFound(t *testing.T) {
	r, mock := newAccountTestRouter(t)
	r.DELETE("/accounts/:id", DeleteAccount)

	userID := testUserID()
	accountID := uuid.New()

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM transactions WHERE account_id").
		WithArgs(accountID, userID).
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectExec("DELETE FROM payees WHERE account_id").
		WithArgs(accountID, userID).
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectExec("DELETE FROM accounts WHERE id").
		WithArgs(accountID, userID).
		WillReturnResult(pgxmock.NewResult("DELETE", 0))

	req, _ := http.NewRequest("DELETE", "/accounts/"+accountID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteAccountInvalidID(t *testing.T) {
	r, mock := newAccountTestRouter(t)
	r.DELETE("/accounts/:id", DeleteAccount)

	req, _ := http.NewRequest("DELETE", "/accounts/not-a-uuid", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestExportAccount(t *testing.T) {
	r, mock := newAccountTestRouter(t)
	r.GET("/accounts/:id/export", ExportAccount)

	userID := testUserID()
	accountID := uuid.New()
	now := time.Now()

	rows := pgxmock.NewRows([]string{"date", "description", "amount", "type", "tags", "notes"}).
		AddRow(now, "Coffee", 250.5, "debit", []string{"food"}, "morning").
		AddRow(now.AddDate(0, 0, -1), "Salary", 50000.0, "credit", nil, "")

	mock.ExpectQuery("SELECT t.date, t.description, t.amount, t.type, t.tags, t.notes").
		WithArgs(accountID, userID).
		WillReturnRows(rows)

	req, _ := http.NewRequest("GET", "/accounts/"+accountID.String()+"/export", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/csv")
	assert.Contains(t, w.Body.String(), "Date,Description,Amount,Type,Tags,Notes")
	assert.Contains(t, w.Body.String(), "Coffee")
	assert.Contains(t, w.Body.String(), "Salary")

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestExportAccountInvalidID(t *testing.T) {
	r, mock := newAccountTestRouter(t)
	r.GET("/accounts/:id/export", ExportAccount)

	req, _ := http.NewRequest("GET", "/accounts/not-a-uuid/export", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateAccountPayeeNameConflict(t *testing.T) {
	r, mock := newAccountTestRouter(t)
	r.POST("/accounts", CreateAccount)

	accountID := uuid.New()
	userID := testUserID()
	reqBody := models.CreateAccountRequest{
		Name:          "New Account",
		AccountTypeID: "bank",
		Bank:          "Axis",
	}

	mock.ExpectBegin()

	// Account insert succeeds...
	mock.ExpectQuery("INSERT INTO accounts").
		WithArgs(userID, reqBody.Name, reqBody.AccountTypeID, reqBody.Bank, "INR", "#06b6d4", false, (*int)(nil)).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "account_type_id", "bank", "currency", "color", "is_default", "billing_day", "created_at"}).
			AddRow(accountID, reqBody.Name, reqBody.AccountTypeID, reqBody.Bank, "INR", "#06b6d4", false, intPtr(1), time.Now()))

	mock.ExpectQuery("SELECT name FROM account_types").
		WithArgs(reqBody.AccountTypeID).
		WillReturnRows(pgxmock.NewRows([]string{"name"}).AddRow("Bank Account"))

	// ...but the account-linked payee upsert collides with a payee of the
	// same name owned by this user (payees_user_name_uq) -> unique violation.
	mock.ExpectExec("INSERT INTO payees").
		WithArgs(userID, reqBody.Name, accountID).
		WillReturnError(&pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint \"payees_user_name_uq\""})

	jsonBody, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/accounts", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "payee")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateAccountPayeeNameConflict(t *testing.T) {
	r, mock := newAccountTestRouter(t)
	r.PUT("/accounts/:id", UpdateAccount)

	accountID := uuid.New()
	userID := testUserID()
	isDefault := true
	reqBody := models.UpdateAccountRequest{
		Name:          "Updated Account",
		AccountTypeID: "bank",
		Bank:          "Axis",
		Currency:      "INR",
		Color:         "#06b6d4",
		IsDefault:     &isDefault,
	}

	mock.ExpectBegin()

	mock.ExpectExec("UPDATE accounts SET is_default = FALSE WHERE user_id = \\$1 AND id <> \\$2").
		WithArgs(userID, accountID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	mock.ExpectQuery("WITH updated AS").
		WithArgs(reqBody.Name, reqBody.AccountTypeID, reqBody.Bank, reqBody.Currency, reqBody.Color, &isDefault, accountID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "account_type_id", "account_type_name", "bank", "currency", "color", "is_default", "billing_day", "created_at", "balance"}).
			AddRow(accountID, reqBody.Name, reqBody.AccountTypeID, "Bank Account", reqBody.Bank, reqBody.Currency, reqBody.Color, true, intPtr(1), time.Now(), 0.0))

	// Renaming the account-linked payee collides with another payee of the
	// same name owned by this user (payees_user_name_uq) -> unique violation.
	mock.ExpectExec("UPDATE payees SET name").
		WithArgs(reqBody.Name, accountID, userID).
		WillReturnError(&pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint \"payees_user_name_uq\""})

	body := fmt.Sprintf(`{"name":%q,"accountTypeId":%q,"bank":%q,"currency":%q,"color":%q,"isDefault":true}`,
		reqBody.Name, reqBody.AccountTypeID, reqBody.Bank, reqBody.Currency, reqBody.Color)
	req, _ := http.NewRequest("PUT", "/accounts/"+accountID.String(), bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "payee")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateAccountBillingDayExplicitSet(t *testing.T) {
	r, mock := newAccountTestRouter(t)
	r.PUT("/accounts/:id", UpdateAccount)

	accountID := uuid.New()
	userID := testUserID()

	// billingDay:15 present -> the SQL carries ", billing_day = $9" with 15.
	mock.ExpectBegin()
	mock.ExpectQuery("WITH updated AS").
		WithArgs("Savings", "bank", "Axis", "INR", "#06b6d4", (*bool)(nil), accountID, userID, intPtr(15)).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "account_type_id", "account_type_name", "bank", "currency", "color", "is_default", "billing_day", "created_at", "balance"}).
			AddRow(accountID, "Savings", "bank", "Bank Account", "Axis", "INR", "#06b6d4", false, intPtr(15), time.Now(), 0.0))
	mock.ExpectExec("UPDATE payees SET name").
		WithArgs("Savings", accountID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	req, _ := http.NewRequest("PUT", "/accounts/"+accountID.String(),
		bytes.NewBufferString(`{"name":"Savings","accountTypeId":"bank","bank":"Axis","currency":"INR","color":"#06b6d4","billingDay":15}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var account models.Account
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &account))
	require.NotNil(t, account.BillingDay)
	assert.Equal(t, 15, *account.BillingDay)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateAccountBillingDayExplicitNullClears(t *testing.T) {
	r, mock := newAccountTestRouter(t)
	r.PUT("/accounts/:id", UpdateAccount)

	accountID := uuid.New()
	userID := testUserID()

	// billingDay:null present -> the SQL carries ", billing_day = $9" with nil.
	mock.ExpectBegin()
	mock.ExpectQuery("WITH updated AS").
		WithArgs("Savings", "bank", "Axis", "INR", "#06b6d4", (*bool)(nil), accountID, userID, (*int)(nil)).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "account_type_id", "account_type_name", "bank", "currency", "color", "is_default", "billing_day", "created_at", "balance"}).
			AddRow(accountID, "Savings", "bank", "Bank Account", "Axis", "INR", "#06b6d4", false, (*int)(nil), time.Now(), 0.0))
	mock.ExpectExec("UPDATE payees SET name").
		WithArgs("Savings", accountID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	req, _ := http.NewRequest("PUT", "/accounts/"+accountID.String(),
		bytes.NewBufferString(`{"name":"Savings","accountTypeId":"bank","bank":"Axis","currency":"INR","color":"#06b6d4","billingDay":null}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var account models.Account
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &account))
	assert.Nil(t, account.BillingDay)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateAccountBillingDayInvalidRange(t *testing.T) {
	r, mock := newAccountTestRouter(t)
	r.PUT("/accounts/:id", UpdateAccount)

	for _, day := range []int{0, 32, -3} {
		t.Run(fmt.Sprintf("billingDay=%d", day), func(t *testing.T) {
			req, _ := http.NewRequest("PUT", "/accounts/"+uuid.New().String(),
				bytes.NewBufferString(fmt.Sprintf(`{"name":"Savings","billingDay":%d}`, day)))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), "between 1 and 31")
		})
	}
	assert.NoError(t, mock.ExpectationsWereMet())
}
