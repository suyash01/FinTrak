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
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v3"
	"github.com/stretchr/testify/assert"
)

const testJWTSecret = "test-secret"

func newAuthTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(func(c *gin.Context) {
		c.Set("jwtSecret", testJWTSecret)
		c.Next()
	})
	return r
}

func TestRegister(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	r := newAuthTestRouter()
	r.POST("/auth/register", Register)

	userID := uuid.New()
	reqBody := models.RegisterRequest{Email: "test@example.com", Password: "password123"}

	mock.ExpectQuery("INSERT INTO users").
		WithArgs(reqBody.Email, pgxmock.AnyArg(), "user").
		WillReturnRows(pgxmock.NewRows([]string{"id", "email", "role"}).
			AddRow(userID, reqBody.Email, "user"))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM categories").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	seedArgs := make([]interface{}, 24*6)
	for i := range seedArgs {
		seedArgs[i] = pgxmock.AnyArg()
	}
	mock.ExpectExec("INSERT INTO categories").
		WithArgs(seedArgs...).
		WillReturnResult(pgxmock.NewResult("INSERT", 24))

	jsonBody, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/auth/register", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var res models.AuthResponse
	err = json.Unmarshal(w.Body.Bytes(), &res)
	assert.NoError(t, err)
	assert.NotEmpty(t, res.Token)
	assert.Equal(t, userID, res.User.ID)
	assert.Equal(t, reqBody.Email, res.User.Email)
	assert.Equal(t, "user", res.User.Role)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRegisterAdminEmailRequiresSetupToken(t *testing.T) {
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
	r.Use(func(c *gin.Context) {
		c.Set("jwtSecret", testJWTSecret)
		c.Set("adminEmails", []string{"admin@example.com"})
		c.Set("adminSetupToken", "op-secret-token-123")
		c.Next()
	})
	r.POST("/auth/register", Register)

	userID := uuid.New()
	const adminEmail = "admin@example.com"

	run := func(name, setupToken string, wantStatus int, expectInsert bool) {
		t.Run(name, func(t *testing.T) {
			if expectInsert {
				mock.ExpectQuery("INSERT INTO users").
					WithArgs(adminEmail, pgxmock.AnyArg(), "admin").
					WillReturnRows(pgxmock.NewRows([]string{"id", "email", "role"}).
						AddRow(userID, adminEmail, "admin"))
				mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM categories").
					WithArgs(userID).
					WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
				seedArgs := make([]interface{}, 24*6)
				for i := range seedArgs {
					seedArgs[i] = pgxmock.AnyArg()
				}
				mock.ExpectExec("INSERT INTO categories").
					WithArgs(seedArgs...).
					WillReturnResult(pgxmock.NewResult("INSERT", 24))
			}

			// Mixed-case admin email: normalized to lowercase before the
			// allowlist check, so the token gate applies to the identity, not
			// the exact string.
			reqBody := models.RegisterRequest{Email: "ADMIN@example.com", Password: "password123", SetupToken: setupToken}
			jsonBody, _ := json.Marshal(reqBody)
			req, _ := http.NewRequest("POST", "/auth/register", bytes.NewBuffer(jsonBody))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, wantStatus, w.Code)
			if wantStatus == http.StatusCreated {
				var res models.AuthResponse
				assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
				assert.Equal(t, "admin", res.User.Role)
			} else {
				assert.Contains(t, w.Body.String(), "setup token")
			}
		})
	}

	run("missing token", "", http.StatusForbidden, false)
	run("wrong token", "attacker-guess", http.StatusForbidden, false)
	run("correct token", "op-secret-token-123", http.StatusCreated, true)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRegisterAdminEmailWithoutConfiguredToken(t *testing.T) {
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
	r.Use(func(c *gin.Context) {
		c.Set("jwtSecret", testJWTSecret)
		c.Set("adminEmails", []string{"admin@example.com"})
		// adminSetupToken intentionally absent: the operator has not
		// configured one, so admin-listed emails must be refused entirely.
		c.Next()
	})
	r.POST("/auth/register", Register)

	reqBody := models.RegisterRequest{Email: "admin@example.com", Password: "password123", SetupToken: "anything"}
	jsonBody, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/auth/register", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "setup token")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRegisterDuplicateEmail(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	r := newAuthTestRouter()
	r.POST("/auth/register", Register)

	// Mixed-case input is normalized to lowercase before the INSERT, so the
	// case-sensitive UNIQUE constraint on users.email rejects a case-variant
	// duplicate the same way it rejects an identical one (23505 -> 409).
	reqBody := models.RegisterRequest{Email: "DUP@example.com", Password: "password123"}

	mock.ExpectQuery("INSERT INTO users").
		WithArgs("dup@example.com", pgxmock.AnyArg(), "user").
		WillReturnError(&pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint"})

	jsonBody, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/auth/register", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "already exists")

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLogin(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	r := newAuthTestRouter()
	r.POST("/auth/login", Login)

	userID := uuid.New()
	password := "password123"
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}

	reqBody := models.LoginRequest{Email: "test@example.com", Password: password}

	mock.ExpectQuery("SELECT id, email, password_hash, role FROM users").
		WithArgs(reqBody.Email).
		WillReturnRows(pgxmock.NewRows([]string{"id", "email", "password_hash", "role"}).
			AddRow(userID, reqBody.Email, hash, "user"))

	jsonBody, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/auth/login", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var res models.AuthResponse
	err = json.Unmarshal(w.Body.Bytes(), &res)
	assert.NoError(t, err)
	assert.NotEmpty(t, res.Token)
	assert.Equal(t, userID, res.User.ID)
	assert.Equal(t, "user", res.User.Role)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLoginNormalizesEmail(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	r := newAuthTestRouter()
	r.POST("/auth/login", Login)

	userID := uuid.New()
	password := "password123"
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}

	// Mixed-case input is normalized exactly like it is on registration, so
	// the lookup finds the stored lowercase row.
	reqBody := models.LoginRequest{Email: "Test@Example.COM", Password: password}

	mock.ExpectQuery("SELECT id, email, password_hash, role FROM users").
		WithArgs("test@example.com").
		WillReturnRows(pgxmock.NewRows([]string{"id", "email", "password_hash", "role"}).
			AddRow(userID, "test@example.com", hash, "user"))

	jsonBody, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/auth/login", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLoginInvalidPassword(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	r := newAuthTestRouter()
	r.POST("/auth/login", Login)

	userID := uuid.New()
	hash, err := auth.HashPassword("correct-password")
	if err != nil {
		t.Fatal(err)
	}

	reqBody := models.LoginRequest{Email: "test@example.com", Password: "wrong-password"}

	mock.ExpectQuery("SELECT id, email, password_hash, role FROM users").
		WithArgs(reqBody.Email).
		WillReturnRows(pgxmock.NewRows([]string{"id", "email", "password_hash", "role"}).
			AddRow(userID, reqBody.Email, hash, "user"))

	jsonBody, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/auth/login", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLoginUnknownUser(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	r := newAuthTestRouter()
	r.POST("/auth/login", Login)

	reqBody := models.LoginRequest{Email: "missing@example.com", Password: "whatever"}

	mock.ExpectQuery("SELECT id, email, password_hash, role FROM users").
		WithArgs(reqBody.Email).
		WillReturnRows(pgxmock.NewRows([]string{"id", "email", "password_hash", "role"}))

	jsonBody, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/auth/login", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}
