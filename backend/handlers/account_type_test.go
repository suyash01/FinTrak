package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v3"
	"github.com/stretchr/testify/assert"
)

func newAccountTypeRoleRouter(role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(func(c *gin.Context) {
		c.Set("userID", testUserID())
		c.Set("userRole", role)
		c.Next()
	})
	r.GET("/account-types", GetAccountTypes)
	admin := r.Group("/account-types")
	admin.Use(auth.RequireAdmin())
	admin.POST("", CreateAccountType)
	admin.PUT("/:id", UpdateAccountType)
	admin.DELETE("/:id", DeleteAccountType)
	return r
}

func newAccountTypeTestRouter() *gin.Engine {
	return newAccountTypeRoleRouter("admin")
}

func newAccountTypeUserRouter() *gin.Engine {
	return newAccountTypeRoleRouter("user")
}

func TestGetAccountTypes(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	r := newAccountTypeTestRouter()

	rows := pgxmock.NewRows([]string{"id", "name", "positive_txn_type"}).
		AddRow("bank", "Bank Account", "credit").
		AddRow("credit_card", "Credit Card", "debit")

	mock.ExpectQuery("SELECT id, name, positive_txn_type FROM account_types ORDER BY name").
		WillReturnRows(rows)

	req, _ := http.NewRequest(http.MethodGet, "/account-types", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var types []models.AccountType
	err = json.Unmarshal(w.Body.Bytes(), &types)
	assert.NoError(t, err)
	assert.Len(t, types, 2)
	assert.Equal(t, "bank", types[0].ID)
	assert.Equal(t, "Credit Card", types[1].Name)
	assert.Equal(t, "debit", types[1].PositiveTxnType)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetAccountTypesQueryError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	r := newAccountTypeTestRouter()

	mock.ExpectQuery("SELECT id, name, positive_txn_type FROM account_types").
		WillReturnError(assert.AnError)

	req, _ := http.NewRequest(http.MethodGet, "/account-types", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateAccountType(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()

		oldPool := db.Pool
		db.Pool = mock
		defer func() { db.Pool = oldPool }()

		r := newAccountTypeTestRouter()
		reqBody := models.CreateAccountTypeRequest{ID: "savings", Name: "Savings", PositiveTxnType: "credit"}

		mock.ExpectQuery("INSERT INTO account_types").
			WithArgs(reqBody.ID, reqBody.Name, reqBody.PositiveTxnType).
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "positive_txn_type"}).
				AddRow(reqBody.ID, reqBody.Name, reqBody.PositiveTxnType))

		jsonBody, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest(http.MethodPost, "/account-types", bytes.NewBuffer(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)

		var at models.AccountType
		err = json.Unmarshal(w.Body.Bytes(), &at)
		assert.NoError(t, err)
		assert.Equal(t, "savings", at.ID)
		assert.Equal(t, "Savings", at.Name)

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("invalid json", func(t *testing.T) {
		r := newAccountTypeTestRouter()

		req, _ := http.NewRequest(http.MethodPost, "/account-types", bytes.NewBufferString("{invalid"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid positive txn type", func(t *testing.T) {
		r := newAccountTypeTestRouter()
		reqBody := models.CreateAccountTypeRequest{ID: "savings", Name: "Savings", PositiveTxnType: "both"}

		jsonBody, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest(http.MethodPost, "/account-types", bytes.NewBuffer(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "credit")
	})

	t.Run("database error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()

		oldPool := db.Pool
		db.Pool = mock
		defer func() { db.Pool = oldPool }()

		r := newAccountTypeTestRouter()
		reqBody := models.CreateAccountTypeRequest{ID: "savings", Name: "Savings", PositiveTxnType: "credit"}

		mock.ExpectQuery("INSERT INTO account_types").
			WithArgs(reqBody.ID, reqBody.Name, reqBody.PositiveTxnType).
			WillReturnError(assert.AnError)

		jsonBody, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest(http.MethodPost, "/account-types", bytes.NewBuffer(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestUpdateAccountType(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()

		oldPool := db.Pool
		db.Pool = mock
		defer func() { db.Pool = oldPool }()

		r := newAccountTypeTestRouter()
		reqBody := models.UpdateAccountTypeRequest{Name: "Bank", PositiveTxnType: "credit"}

		mock.ExpectQuery("UPDATE account_types").
			WithArgs(reqBody.Name, reqBody.PositiveTxnType, "savings").
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "positive_txn_type"}).
				AddRow("savings", reqBody.Name, reqBody.PositiveTxnType))

		jsonBody, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest(http.MethodPut, "/account-types/savings", bytes.NewBuffer(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()

		oldPool := db.Pool
		db.Pool = mock
		defer func() { db.Pool = oldPool }()

		r := newAccountTypeTestRouter()
		reqBody := models.UpdateAccountTypeRequest{Name: "Nope"}

		mock.ExpectQuery("UPDATE account_types").
			WithArgs(reqBody.Name, reqBody.PositiveTxnType, "missing").
			WillReturnError(pgx.ErrNoRows)

		jsonBody, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest(http.MethodPut, "/account-types/missing", bytes.NewBuffer(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("invalid json", func(t *testing.T) {
		r := newAccountTypeTestRouter()

		req, _ := http.NewRequest(http.MethodPut, "/account-types/savings", bytes.NewBufferString("{"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid positive txn type", func(t *testing.T) {
		r := newAccountTypeTestRouter()
		reqBody := models.UpdateAccountTypeRequest{PositiveTxnType: "invalid"}

		jsonBody, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest(http.MethodPut, "/account-types/savings", bytes.NewBuffer(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestDeleteAccountType(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()

		oldPool := db.Pool
		db.Pool = mock
		defer func() { db.Pool = oldPool }()

		r := newAccountTypeTestRouter()

		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE account_type_id").
			WithArgs("savings").
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
		mock.ExpectExec("DELETE FROM account_types WHERE id").
			WithArgs("savings").
			WillReturnResult(pgxmock.NewResult("DELETE", 1))

		req, _ := http.NewRequest(http.MethodDelete, "/account-types/savings", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "deleted")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("in use by accounts", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()

		oldPool := db.Pool
		db.Pool = mock
		defer func() { db.Pool = oldPool }()

		r := newAccountTypeTestRouter()

		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE account_type_id").
			WithArgs("savings").
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(3))

		req, _ := http.NewRequest(http.MethodDelete, "/account-types/savings", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusConflict, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()

		oldPool := db.Pool
		db.Pool = mock
		defer func() { db.Pool = oldPool }()

		r := newAccountTypeTestRouter()

		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE account_type_id").
			WithArgs("missing").
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
		mock.ExpectExec("DELETE FROM account_types WHERE id").
			WithArgs("missing").
			WillReturnResult(pgxmock.NewResult("DELETE", 0))

		req, _ := http.NewRequest(http.MethodDelete, "/account-types/missing", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestAccountTypeMutationsRequireAdmin(t *testing.T) {
	r := newAccountTypeUserRouter()

	createBody := `{"id":"savings","name":"Savings","positiveTxnType":"credit"}`
	updateBody := `{"name":"Savings"}`

	tests := []struct {
		name string
		req  *http.Request
	}{
		{name: "create", req: mustJSONRequest(http.MethodPost, "/account-types", createBody)},
		{name: "update", req: mustJSONRequest(http.MethodPut, "/account-types/savings", updateBody)},
		{name: "delete", req: mustJSONRequest(http.MethodDelete, "/account-types/savings", "")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, tt.req)
			assert.Equal(t, http.StatusForbidden, w.Code)
		})
	}
}

func TestAccountTypeBuiltInProtected(t *testing.T) {
	createBody := `{"id":"bank","name":"Bank","positiveTxnType":"credit"}`
	updateBody := `{"name":"Bank"}`

	tests := []struct {
		name string
		req  *http.Request
	}{
		{name: "create", req: mustJSONRequest(http.MethodPost, "/account-types", createBody)},
		{name: "update", req: mustJSONRequest(http.MethodPut, "/account-types/credit_card", updateBody)},
		{name: "delete", req: mustJSONRequest(http.MethodDelete, "/account-types/bank", "")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newAccountTypeTestRouter()
			w := httptest.NewRecorder()
			r.ServeHTTP(w, tt.req)
			assert.Equal(t, http.StatusForbidden, w.Code)
			assert.Contains(t, w.Body.String(), "built-in")
		})
	}
}

func TestCreateAccountTypeInvalidID(t *testing.T) {
	r := newAccountTypeTestRouter()

	for _, id := range []string{"1savings", "Bad-ID", "savings account", "x"} {
		body := `{"id":"` + id + `","name":"X","positiveTxnType":"credit"}`
		req := mustJSONRequest(http.MethodPost, "/account-types", body)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code, "id %q should be rejected", id)
	}
}

func mustJSONRequest(method, path, body string) *http.Request {
	req, _ := http.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}
