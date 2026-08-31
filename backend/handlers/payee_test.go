package handlers

import (
	"bytes"
	"encoding/json"
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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v3"
	"github.com/stretchr/testify/assert"
)

func newPayeeTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.GET("/payees", GetPayees)
	r.POST("/payees", CreatePayee)
	r.PUT("/payees/:id", UpdatePayee)
	r.DELETE("/payees/:id", DeletePayee)
	return r
}

func payeeRows(rows ...[]interface{}) *pgxmock.Rows {
	return pgxmock.NewRows([]string{"id", "name", "account_id", "created_at", "updated_at"}).AddRows(rows...)
}

func TestGetPayees(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	r := newPayeeTestRouter()
	userID := testUserID()
	now := time.Now()
	accountID := uuid.New()

	mock.ExpectQuery("SELECT id, name, account_id, created_at, updated_at FROM payees WHERE user_id").
		WithArgs(userID).
		WillReturnRows(payeeRows(
			[]interface{}{uuid.New(), "Amazon", &accountID, now, now},
			[]interface{}{uuid.New(), "Swiggy", nil, now, now},
		))

	req, _ := http.NewRequest(http.MethodGet, "/payees", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var payees []models.Payee
	err = json.Unmarshal(w.Body.Bytes(), &payees)
	assert.NoError(t, err)
	assert.Len(t, payees, 2)
	assert.Equal(t, "Amazon", payees[0].Name)
	assert.NotNil(t, payees[0].AccountID)
	assert.Equal(t, accountID, *payees[0].AccountID)
	assert.Nil(t, payees[1].AccountID)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetPayeesQueryError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	r := newPayeeTestRouter()

	mock.ExpectQuery("SELECT id, name, account_id, created_at, updated_at FROM payees").
		WithArgs(testUserID()).
		WillReturnError(assert.AnError)

	req, _ := http.NewRequest(http.MethodGet, "/payees", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreatePayee(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()

		oldPool := db.Pool
		db.Pool = mock
		defer func() { db.Pool = oldPool }()

		r := newPayeeTestRouter()
		userID := testUserID()
		now := time.Now()

		payeeID := uuid.New()
		reqBody := models.CreatePayeeRequest{Name: "Amazon"}

		mock.ExpectQuery("INSERT INTO payees").
			WithArgs(userID, reqBody.Name, pgxmock.AnyArg()).
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "account_id", "created_at", "updated_at"}).
				AddRow(payeeID, reqBody.Name, nil, now, now))

		jsonBody, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest(http.MethodPost, "/payees", bytes.NewBuffer(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)

		var p models.Payee
		err = json.Unmarshal(w.Body.Bytes(), &p)
		assert.NoError(t, err)
		assert.Equal(t, payeeID, p.ID)
		assert.Equal(t, "Amazon", p.Name)

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("duplicate name", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()

		oldPool := db.Pool
		db.Pool = mock
		defer func() { db.Pool = oldPool }()

		r := newPayeeTestRouter()

		mock.ExpectQuery("INSERT INTO payees").
			WithArgs(testUserID(), "Amazon", pgxmock.AnyArg()).
			WillReturnError(&pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint"})

		reqBody := models.CreatePayeeRequest{Name: "Amazon"}
		jsonBody, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest(http.MethodPost, "/payees", bytes.NewBuffer(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusConflict, w.Code)
		assert.Contains(t, w.Body.String(), "already exists")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("invalid json", func(t *testing.T) {
		r := newPayeeTestRouter()

		req, _ := http.NewRequest(http.MethodPost, "/payees", bytes.NewBufferString("{"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
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

		r := newPayeeTestRouter()

		mock.ExpectQuery("INSERT INTO payees").
			WithArgs(testUserID(), "Amazon", pgxmock.AnyArg()).
			WillReturnError(assert.AnError)

		reqBody := models.CreatePayeeRequest{Name: "Amazon"}
		jsonBody, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest(http.MethodPost, "/payees", bytes.NewBuffer(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestUpdatePayee(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()

		oldPool := db.Pool
		db.Pool = mock
		defer func() { db.Pool = oldPool }()

		r := newPayeeTestRouter()
		userID := testUserID()
		now := time.Now()
		payeeID := uuid.New()
		reqBody := models.CreatePayeeRequest{Name: "Renamed"}

		mock.ExpectQuery("UPDATE payees").
			WithArgs(reqBody.Name, pgxmock.AnyArg(), payeeID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "account_id", "created_at", "updated_at"}).
				AddRow(payeeID, reqBody.Name, nil, now, now))

		jsonBody, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest(http.MethodPut, "/payees/"+payeeID.String(), bytes.NewBuffer(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("invalid id", func(t *testing.T) {
		r := newPayeeTestRouter()

		req, _ := http.NewRequest(http.MethodPut, "/payees/not-a-uuid", bytes.NewBufferString(`{"name":"x"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
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

		r := newPayeeTestRouter()
		payeeID := uuid.New()
		reqBody := models.CreatePayeeRequest{Name: "Ghost"}

		mock.ExpectQuery("UPDATE payees").
			WithArgs(reqBody.Name, pgxmock.AnyArg(), payeeID, testUserID()).
			WillReturnError(pgx.ErrNoRows)

		jsonBody, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest(http.MethodPut, "/payees/"+payeeID.String(), bytes.NewBuffer(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("duplicate name", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()

		oldPool := db.Pool
		db.Pool = mock
		defer func() { db.Pool = oldPool }()

		r := newPayeeTestRouter()
		payeeID := uuid.New()
		reqBody := models.CreatePayeeRequest{Name: "Amazon"}

		mock.ExpectQuery("UPDATE payees").
			WithArgs(reqBody.Name, pgxmock.AnyArg(), payeeID, testUserID()).
			WillReturnError(&pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint"})

		jsonBody, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest(http.MethodPut, "/payees/"+payeeID.String(), bytes.NewBuffer(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusConflict, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestDeletePayee(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()

		oldPool := db.Pool
		db.Pool = mock
		defer func() { db.Pool = oldPool }()

		r := newPayeeTestRouter()
		payeeID := uuid.New()

		mock.ExpectExec("DELETE FROM payees").
			WithArgs(payeeID, testUserID()).
			WillReturnResult(pgxmock.NewResult("DELETE", 1))

		req, _ := http.NewRequest(http.MethodDelete, "/payees/"+payeeID.String(), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "deleted")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("invalid id", func(t *testing.T) {
		r := newPayeeTestRouter()

		req, _ := http.NewRequest(http.MethodDelete, "/payees/not-a-uuid", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
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

		r := newPayeeTestRouter()
		payeeID := uuid.New()

		mock.ExpectExec("DELETE FROM payees").
			WithArgs(payeeID, testUserID()).
			WillReturnResult(pgxmock.NewResult("DELETE", 0))

		req, _ := http.NewRequest(http.MethodDelete, "/payees/"+payeeID.String(), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCreatePayeeNullAccountGuardIsSelfTyped(t *testing.T) {
	// Regression: the INSERT...SELECT ownership guard must self-type $3 with
	// an explicit ::uuid cast. Without it, PostgreSQL fails statement Prepare
	// with 42P08 ("could not determine data type of parameter $3") whenever a
	// payee is created with accountId null/absent -> 500 on every such create.
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	r := newPayeeTestRouter()
	userID := testUserID()
	now := time.Now()
	payeeID := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta("WHERE ($3::uuid IS NULL OR EXISTS (SELECT 1 FROM accounts a WHERE a.id = $3 AND a.user_id = $1))")).
		WithArgs(userID, "Amazon", pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "account_id", "created_at", "updated_at"}).
			AddRow(payeeID, "Amazon", nil, now, now))

	// accountId omitted entirely (nil) — the case that used to 500.
	jsonBody, _ := json.Marshal(models.CreatePayeeRequest{Name: "Amazon"})
	req, _ := http.NewRequest(http.MethodPost, "/payees", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdatePayeeNullAccountGuardIsSelfTyped(t *testing.T) {
	// Same 42P08 guard as CreatePayee: unsetting the account link (accountId
	// null) must type $2 explicitly or Prepare fails for every such update.
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	r := newPayeeTestRouter()
	userID := testUserID()
	now := time.Now()
	payeeID := uuid.New()

	mock.ExpectQuery(regexp.QuoteMeta("AND ($2::uuid IS NULL OR EXISTS (SELECT 1 FROM accounts a WHERE a.id = $2 AND a.user_id = $4))")).
		WithArgs("Renamed", pgxmock.AnyArg(), payeeID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "account_id", "created_at", "updated_at"}).
			AddRow(payeeID, "Renamed", nil, now, now))

	jsonBody, _ := json.Marshal(models.CreatePayeeRequest{Name: "Renamed"})
	req, _ := http.NewRequest(http.MethodPut, "/payees/"+payeeID.String(), bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreatePayeeAccountNotOwned(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	r := newPayeeTestRouter()
	otherAccountID := uuid.New()
	reqBody := models.CreatePayeeRequest{Name: "Amazon", AccountID: &otherAccountID}

	// Account belongs to another user -> INSERT...SELECT matches no rows.
	mock.ExpectQuery("INSERT INTO payees").
		WithArgs(testUserID(), reqBody.Name, &otherAccountID).
		WillReturnError(pgx.ErrNoRows)

	jsonBody, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest(http.MethodPost, "/payees", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdatePayeeAccountNotOwned(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	r := newPayeeTestRouter()
	payeeID := uuid.New()
	otherAccountID := uuid.New()
	reqBody := models.CreatePayeeRequest{Name: "Renamed", AccountID: &otherAccountID}

	// Account belongs to another user -> no row matches.
	mock.ExpectQuery("UPDATE payees").
		WithArgs(reqBody.Name, &otherAccountID, payeeID, testUserID()).
		WillReturnError(pgx.ErrNoRows)

	jsonBody, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest(http.MethodPut, "/payees/"+payeeID.String(), bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}
