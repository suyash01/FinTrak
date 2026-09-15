package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/internal/ratelimit"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
	"golang.org/x/time/rate"
)

const testJWTSecret = "test-secret"

// TestMain installs the JWT secret the auth handlers read from package config
// (secrets are no longer carried in the request context).
func TestMain(m *testing.M) {
	SetJWTSecret(testJWTSecret)
	os.Exit(m.Run())
}

func newAuthTestRouter(srv *Server) *gin.Engine {
	gin.SetMode(gin.TestMode)
	return gin.Default()
}

func TestRegister(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	r := newAuthTestRouter(srv)
	r.POST("/auth/register", srv.Register)

	userID := uuid.New()
	reqBody := models.RegisterRequest{Email: "test@example.com", Password: "password1234"}

	mock.ExpectBegin()
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
	mock.ExpectCommit()

	jsonBody, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/auth/register", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var res models.AuthResponse
	err = json.Unmarshal(w.Body.Bytes(), &res)
	assert.NoError(t, err)
	assert.Equal(t, userID, res.User.ID)
	assert.Equal(t, reqBody.Email, res.User.Email)
	assert.Equal(t, "user", res.User.Role)

	// The JWT is delivered as an httpOnly session cookie, not in the body.
	cookies := w.Result().Cookies()
	assert.Len(t, cookies, 1)
	assert.Equal(t, auth.AuthCookieName, cookies[0].Name)
	assert.True(t, cookies[0].HttpOnly)
	assert.NotEmpty(t, cookies[0].Value)
	assert.NotContains(t, w.Body.String(), "token")

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRegisterRejectsWeakPassword(t *testing.T) {
	srv := newTestServer(nil)
	r := newAuthTestRouter(srv)
	r.POST("/auth/register", srv.Register)

	post := func(password string) *httptest.ResponseRecorder {
		reqBody := models.RegisterRequest{Email: "weak@example.com", Password: password}
		jsonBody, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", "/auth/register", bytes.NewBuffer(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	t.Run("too short", func(t *testing.T) {
		w := post("short")
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "at least 12")
	})

	t.Run("too many bytes", func(t *testing.T) {
		w := post(strings.Repeat("a", 73))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "at most 72 bytes")
	})
}

func TestLoginAllowsLegacyShortPassword(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	r := newAuthTestRouter(srv)
	r.POST("/auth/login", srv.Login)

	userID := uuid.New()
	// A 6-character password predates the stronger registration policy; login
	// must not reject it at the binding layer.
	password := "abcdef"
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}

	reqBody := models.LoginRequest{Email: "legacy@example.com", Password: password}
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
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRegisterRollsBackWhenCategorySeedFails(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	r := newAuthTestRouter(srv)
	r.POST("/auth/register", srv.Register)

	userID := uuid.New()
	reqBody := models.RegisterRequest{Email: "seedfail@example.com", Password: "password1234"}

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO users").
		WithArgs(reqBody.Email, pgxmock.AnyArg(), "user").
		WillReturnRows(pgxmock.NewRows([]string{"id", "email", "role"}).
			AddRow(userID, reqBody.Email, "user"))
	// Seeding fails -> the whole registration must roll back (no half-created
	// user without default categories).
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM categories").
		WithArgs(userID).
		WillReturnError(assert.AnError)
	mock.ExpectRollback()

	jsonBody, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/auth/register", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Empty(t, w.Result().Cookies(), "no session cookie is issued on rollback")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRegisterAdminEmailRequiresSetupToken(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	SetAdminConfig([]string{"admin@example.com"}, "op-secret-token-123")
	t.Cleanup(func() { SetAdminConfig(nil, "") })

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.POST("/auth/register", srv.Register)

	userID := uuid.New()
	const adminEmail = "admin@example.com"

	run := func(name, setupToken string, wantStatus int, expectInsert bool) {
		t.Run(name, func(t *testing.T) {
			if expectInsert {
				mock.ExpectBegin()
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
				mock.ExpectCommit()
			}

			// Mixed-case admin email: normalized to lowercase before the
			// allowlist check, so the token gate applies to the identity, not
			// the exact string.
			reqBody := models.RegisterRequest{Email: "ADMIN@example.com", Password: "password1234", SetupToken: setupToken}
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
	srv := newTestServer(mock)

	SetAdminConfig([]string{"admin@example.com"}, "")
	t.Cleanup(func() { SetAdminConfig(nil, "") })

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.POST("/auth/register", srv.Register)

	reqBody := models.RegisterRequest{Email: "admin@example.com", Password: "password1234", SetupToken: "anything"}
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
	srv := newTestServer(mock)

	r := newAuthTestRouter(srv)
	r.POST("/auth/register", srv.Register)

	// Mixed-case input is normalized to lowercase before the INSERT, so the
	// case-sensitive UNIQUE constraint on users.email rejects a case-variant
	// duplicate the same way it rejects an identical one (23505 -> 409).
	reqBody := models.RegisterRequest{Email: "DUP@example.com", Password: "password1234"}

	mock.ExpectBegin()
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
	srv := newTestServer(mock)

	r := newAuthTestRouter(srv)
	r.POST("/auth/login", srv.Login)

	userID := uuid.New()
	password := "password1234"
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
	assert.Equal(t, userID, res.User.ID)
	assert.Equal(t, "user", res.User.Role)
	assert.Len(t, w.Result().Cookies(), 1)
	assert.Equal(t, auth.AuthCookieName, w.Result().Cookies()[0].Name)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLoginNormalizesEmail(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	r := newAuthTestRouter(srv)
	r.POST("/auth/login", srv.Login)

	userID := uuid.New()
	password := "password1234"
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
	srv := newTestServer(mock)

	r := newAuthTestRouter(srv)
	r.POST("/auth/login", srv.Login)

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

func TestLoginRateLimited(t *testing.T) {
	// A zero-refill, single-token bucket means the second request from the same
	// IP is rejected before it reaches the database.
	SetAuthRateLimiter(ratelimit.New(ratelimit.Config{Rate: rate.Limit(0), Burst: 1, TTL: time.Minute}))
	t.Cleanup(func() { SetAuthRateLimiter(nil) })

	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	r := newAuthTestRouter(srv)
	r.POST("/auth/login", srv.Login)

	reqBody := models.LoginRequest{Email: "limited@example.com", Password: "whatever"}
	jsonBody, _ := json.Marshal(reqBody)

	// First attempt is allowed through to the (empty) user lookup.
	mock.ExpectQuery("SELECT id, email, password_hash, role FROM users").
		WithArgs(reqBody.Email).
		WillReturnRows(pgxmock.NewRows([]string{"id", "email", "password_hash", "role"}))

	req, _ := http.NewRequest("POST", "/auth/login", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// Second attempt is throttled; no further DB query is expected.
	req, _ = http.NewRequest("POST", "/auth/login", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Equal(t, "30", w.Header().Get("Retry-After"))

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLoginUnknownUser(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	r := newAuthTestRouter(srv)
	r.POST("/auth/login", srv.Login)

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

func TestLogout(t *testing.T) {
	SetCookieSecure(true)
	t.Cleanup(func() { SetCookieSecure(false) })

	gin.SetMode(gin.TestMode)
	r := gin.New()
	srv := newTestServer(nil)
	r.POST("/auth/logout", srv.Logout)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/logout", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	cookies := w.Result().Cookies()
	assert.Len(t, cookies, 1)
	assert.Equal(t, auth.AuthCookieName, cookies[0].Name)
	assert.Equal(t, "", cookies[0].Value)
	assert.Less(t, cookies[0].MaxAge, 0)
}

func TestMe(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(testAuthMiddleware())
	r.GET("/auth/me", srv.Me)

	userID := testUserID()
	mock.ExpectQuery("SELECT id, email, role FROM users").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "email", "role"}).
			AddRow(userID, "me@example.com", "admin"))

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/auth/me", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var user models.User
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &user))
	assert.Equal(t, userID, user.ID)
	assert.Equal(t, "me@example.com", user.Email)
	assert.Equal(t, "admin", user.Role)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMeErrors(t *testing.T) {
	t.Run("user not found", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()
		srv := newTestServer(mock)

		gin.SetMode(gin.TestMode)
		r := gin.New()
		r.Use(testAuthMiddleware())
		r.GET("/auth/me", srv.Me)

		mock.ExpectQuery("SELECT id, email, role FROM users").
			WithArgs(testUserID()).
			WillReturnError(pgx.ErrNoRows)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/auth/me", nil))

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("database error", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		if err != nil {
			t.Fatal(err)
		}
		defer mock.Close()
		srv := newTestServer(mock)

		gin.SetMode(gin.TestMode)
		r := gin.New()
		r.Use(testAuthMiddleware())
		r.GET("/auth/me", srv.Me)

		mock.ExpectQuery("SELECT id, email, role FROM users").
			WithArgs(testUserID()).
			WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/auth/me", nil))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
