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
	seedArgs := make([]interface{}, 25*6)
	for i := range seedArgs {
		seedArgs[i] = pgxmock.AnyArg()
	}
	mock.ExpectExec("INSERT INTO categories").
		WithArgs(seedArgs...).
		WillReturnResult(pgxmock.NewResult("INSERT", 25))

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

func TestRegisterAdminEmail(t *testing.T) {
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
		c.Next()
	})
	r.POST("/auth/register", Register)

	userID := uuid.New()
	reqBody := models.RegisterRequest{Email: "ADMIN@example.com", Password: "password123"}

	mock.ExpectQuery("INSERT INTO users").
		WithArgs(reqBody.Email, pgxmock.AnyArg(), "admin").
		WillReturnRows(pgxmock.NewRows([]string{"id", "email", "role"}).
			AddRow(userID, reqBody.Email, "admin"))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM categories").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	seedArgs := make([]interface{}, 25*6)
	for i := range seedArgs {
		seedArgs[i] = pgxmock.AnyArg()
	}
	mock.ExpectExec("INSERT INTO categories").
		WithArgs(seedArgs...).
		WillReturnResult(pgxmock.NewResult("INSERT", 25))

	jsonBody, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/auth/register", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var res models.AuthResponse
	err = json.Unmarshal(w.Body.Bytes(), &res)
	assert.NoError(t, err)
	assert.Equal(t, "admin", res.User.Role)

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

	reqBody := models.RegisterRequest{Email: "dup@example.com", Password: "password123"}

	mock.ExpectQuery("INSERT INTO users").
		WithArgs(reqBody.Email, pgxmock.AnyArg(), "user").
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
