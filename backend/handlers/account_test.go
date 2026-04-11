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
	"github.com/pashagolub/pgxmock/v3"
	"github.com/stretchr/testify/assert"
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
	r.GET("/accounts", GetAccounts)

	// Define expected data
	rows := pgxmock.NewRows([]string{"id", "name", "type", "bank", "currency", "color", "balance"}).
		AddRow(uuid.New(), "Savings", "bank", "HDFC", "INR", "#000000", 1000.50).
		AddRow(uuid.New(), "Credit Card", "credit_card", "SBI", "INR", "#ff0000", 500.00)

	mock.ExpectQuery("SELECT a.id, a.name, a.type, a.bank, a.currency, a.color").
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
	r.POST("/accounts", CreateAccount)

	accountID := uuid.New()
	reqBody := models.CreateAccountRequest{
		Name: "New Account",
		Type: "bank",
		Bank: "Axis",
	}

	// Expect Transaction
	mock.ExpectBegin()
	
	// Expect Insert Account
	mock.ExpectQuery("INSERT INTO accounts").
		WithArgs(reqBody.Name, reqBody.Type, reqBody.Bank, "INR", "#06b6d4").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "type", "bank", "currency", "color"}).
			AddRow(accountID, reqBody.Name, reqBody.Type, reqBody.Bank, "INR", "#06b6d4"))

	// Expect Insert/Update Payee
	mock.ExpectExec("INSERT INTO payees").
		WithArgs(reqBody.Name, accountID).
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
