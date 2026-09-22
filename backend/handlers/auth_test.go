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
	"github.com/stretchr/testify/require"
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

// expectRefreshTokenInsert registers the pgxmock expectation for the row that
// records a freshly issued refresh token (Login/Register start a new family).
func expectRefreshTokenInsert(mock pgxmock.PgxPoolIface) {
	mock.ExpectExec("INSERT INTO refresh_tokens").
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
}

// refreshRequest builds a POST /auth/refresh carrying the given refresh cookie.
func refreshRequest(token string) *http.Request {
	return cookieRequest(http.MethodPost, "/auth/refresh", token)
}

// cookieRequest builds a request carrying the given refresh cookie, so the
// logout tests can present a token the browser's jar would have dropped.
func cookieRequest(method, path, token string) *http.Request {
	req, _ := http.NewRequest(method, path, nil)
	if token != "" {
		req.AddCookie(&http.Cookie{Name: auth.RefreshCookieName, Value: token})
	}
	return req
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
	// The new session is recorded server-side so it can be rotated/revoked.
	expectRefreshTokenInsert(mock)

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

	// The tokens are delivered as httpOnly cookies, not in the body.
	cookies := w.Result().Cookies()
	assert.Len(t, cookies, 2)
	assert.Equal(t, auth.AccessCookieName, cookies[0].Name)
	assert.Equal(t, auth.RefreshCookieName, cookies[1].Name)
	assert.True(t, cookies[0].HttpOnly)
	assert.True(t, cookies[1].HttpOnly)
	assert.NotEmpty(t, cookies[0].Value)
	assert.NotEmpty(t, cookies[1].Value)
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
	expectRefreshTokenInsert(mock)

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
				expectRefreshTokenInsert(mock)
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
				return
			}
			// The refusal must not confirm that this address is reserved, and
			// must never mention the setup token: both would let an
			// unauthenticated caller enumerate ADMIN_EMAILS. It is answered
			// exactly like a taken address instead.
			assert.Equal(t, http.StatusConflict, w.Code)
			assert.NotContains(t, w.Body.String(), "setup token")
			assert.NotContains(t, w.Body.String(), "admin")
			assert.JSONEq(t, `{"errors":[{"message":"an account with this email already exists"}]}`, w.Body.String())
		})
	}

	run("missing token", "", http.StatusConflict, false)
	run("wrong token", "attacker-guess", http.StatusConflict, false)
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

	// No setup token is configured, so the reserved address cannot self-register
	// at all — and the caller must not be able to tell that apart from an
	// address that is simply taken.
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.NotContains(t, w.Body.String(), "setup token")
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
	expectRefreshTokenInsert(mock)

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
	cookies := w.Result().Cookies()
	assert.Len(t, cookies, 2)
	assert.Equal(t, auth.AccessCookieName, cookies[0].Name)
	assert.Equal(t, auth.RefreshCookieName, cookies[1].Name)

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
	expectRefreshTokenInsert(mock)

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
	assert.Len(t, cookies, 2)
	assert.Equal(t, auth.AccessCookieName, cookies[0].Name)
	assert.Equal(t, auth.RefreshCookieName, cookies[1].Name)
	for _, ck := range cookies {
		assert.Equal(t, "", ck.Value)
		assert.Less(t, ck.MaxAge, 0)
	}
}

// TestLogoutRevokesSessionFamily pins the revocation half of finding 3: logout
// used to only expire the browser's copy of the cookie, so a stolen refresh
// token kept minting access tokens for the rest of the session's 30 days.
func TestLogoutRevokesSessionFamily(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	srv := newTestServer(mock)
	r := gin.New()
	r.POST("/auth/logout", srv.Logout)

	token, err := auth.GenerateRefreshToken(uuid.New(), "user", testJWTSecret)
	require.NoError(t, err)
	familyID := uuid.New()

	mock.ExpectQuery("SELECT family_id FROM refresh_tokens WHERE token_hash = ").
		WithArgs(auth.HashRefreshToken(token)).
		WillReturnRows(pgxmock.NewRows([]string{"family_id"}).AddRow(familyID))
	mock.ExpectExec("UPDATE refresh_tokens SET revoked_at = now\\(\\) WHERE family_id = ").
		WithArgs(familyID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 2))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, cookieRequest(http.MethodPost, "/auth/logout", token))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A logout whose token is unknown (never issued, or already pruned) is still a
// successful logout: the cookies are cleared and no revocation is attempted.
func TestLogoutWithUnknownTokenStillClearsCookies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	srv := newTestServer(mock)
	r := gin.New()
	r.POST("/auth/logout", srv.Logout)

	mock.ExpectQuery("SELECT family_id FROM refresh_tokens WHERE token_hash = ").
		WithArgs(pgxmock.AnyArg()).
		WillReturnError(pgx.ErrNoRows)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, cookieRequest(http.MethodPost, "/auth/logout", "some-token"))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Len(t, w.Result().Cookies(), 2)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A database failure while revoking must not turn logout into an error: the
// browser's cookies are cleared regardless, which is the part the user asked
// for. (The session row is then left for reuse detection to catch.)
func TestLogoutToleratesRevocationFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	srv := newTestServer(mock)
	r := gin.New()
	r.POST("/auth/logout", srv.Logout)

	mock.ExpectQuery("SELECT family_id FROM refresh_tokens WHERE token_hash = ").
		WithArgs(pgxmock.AnyArg()).
		WillReturnError(assert.AnError)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, cookieRequest(http.MethodPost, "/auth/logout", "some-token"))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Len(t, w.Result().Cookies(), 2)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func findAuthCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, ck := range cookies {
		if ck.Name == name {
			return ck
		}
	}
	return nil
}

// expectLiveRefreshSession registers the expectations for looking up a live
// refresh token row and reading the owner's role.
func expectLiveRefreshSession(mock pgxmock.PgxPoolIface, tokenHash string, tokenID, userID, familyID uuid.UUID, role string) {
	mock.ExpectQuery("SELECT id, user_id, family_id, revoked_at IS NULL FROM refresh_tokens WHERE token_hash = ").
		WithArgs(tokenHash).
		WillReturnRows(pgxmock.NewRows([]string{"id", "user_id", "family_id", "live"}).
			AddRow(tokenID, userID, familyID, true))
	mock.ExpectQuery("SELECT role FROM users WHERE id = ").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"role"}).AddRow(role))
}

// expectRotation registers the expectations for spending the presented token
// and storing its successor in the same family.
func expectRotation(mock pgxmock.PgxPoolIface, tokenID, userID, familyID, newTokenID uuid.UUID) {
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE refresh_tokens SET revoked_at = now\\(\\) WHERE id = ").
		WithArgs(tokenID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectQuery("INSERT INTO refresh_tokens").
		WithArgs(userID, pgxmock.AnyArg(), familyID, pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(newTokenID))
	mock.ExpectExec("UPDATE refresh_tokens SET replaced_by = ").
		WithArgs(tokenID, newTokenID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()
}

func TestRefresh(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	srv := newTestServer(mock)
	r := gin.New()
	r.POST("/auth/refresh", srv.Refresh)

	t.Run("rotates the session and re-issues both cookies", func(t *testing.T) {
		userID := uuid.New()
		refresh, err := auth.GenerateRefreshToken(userID, "user", testJWTSecret)
		require.NoError(t, err)
		tokenID, familyID, newTokenID := uuid.New(), uuid.New(), uuid.New()

		expectLiveRefreshSession(mock, auth.HashRefreshToken(refresh), tokenID, userID, familyID, "user")
		expectRotation(mock, tokenID, userID, familyID, newTokenID)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, refreshRequest(refresh))

		require.Equal(t, http.StatusOK, w.Code)

		access := findAuthCookie(w.Result().Cookies(), auth.AccessCookieName)
		require.NotNil(t, access)
		claims, err := auth.ParseToken(access.Value, testJWTSecret, auth.TokenTypeAccess)
		require.NoError(t, err)
		assert.Equal(t, userID, claims.UserID)
		assert.Equal(t, "user", claims.Role)

		// The refresh cookie IS replaced now: the rotated token is what the
		// browser must present next, and the old value is spent (which is what
		// makes a replay detectable). This supersedes the previous contract,
		// where the refresh cookie was deliberately left untouched.
		rotated := findAuthCookie(w.Result().Cookies(), auth.RefreshCookieName)
		require.NotNil(t, rotated)
		require.NotEmpty(t, rotated.Value)
		assert.NotEqual(t, refresh, rotated.Value)
		rotatedClaims, err := auth.ParseToken(rotated.Value, testJWTSecret, auth.TokenTypeRefresh)
		require.NoError(t, err)
		assert.Equal(t, userID, rotatedClaims.UserID)
		// Rotation never extends the session: the successor expires with the
		// token it replaced.
		require.NotNil(t, claims.ExpiresAt)
		require.NotNil(t, rotatedClaims.ExpiresAt)
		originalClaims, err := auth.ParseToken(refresh, testJWTSecret, auth.TokenTypeRefresh)
		require.NoError(t, err)
		assert.WithinDuration(t, originalClaims.ExpiresAt.Time, rotatedClaims.ExpiresAt.Time, time.Second)

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("re-reads the role so a demotion reaches the new access token", func(t *testing.T) {
		userID := uuid.New()
		refresh, err := auth.GenerateRefreshToken(userID, "admin", testJWTSecret)
		require.NoError(t, err)
		tokenID, familyID, newTokenID := uuid.New(), uuid.New(), uuid.New()

		expectLiveRefreshSession(mock, auth.HashRefreshToken(refresh), tokenID, userID, familyID, "user")
		expectRotation(mock, tokenID, userID, familyID, newTokenID)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, refreshRequest(refresh))

		require.Equal(t, http.StatusOK, w.Code)
		access := findAuthCookie(w.Result().Cookies(), auth.AccessCookieName)
		require.NotNil(t, access)
		claims, err := auth.ParseToken(access.Value, testJWTSecret, auth.TokenTypeAccess)
		require.NoError(t, err)
		assert.Equal(t, "user", claims.Role, "the database role must win over the token's claim")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("a deleted account ends the session", func(t *testing.T) {
		userID := uuid.New()
		refresh, err := auth.GenerateRefreshToken(userID, "user", testJWTSecret)
		require.NoError(t, err)

		mock.ExpectQuery("SELECT id, user_id, family_id, revoked_at IS NULL FROM refresh_tokens WHERE token_hash = ").
			WithArgs(auth.HashRefreshToken(refresh)).
			WillReturnRows(pgxmock.NewRows([]string{"id", "user_id", "family_id", "live"}).
				AddRow(uuid.New(), userID, uuid.New(), true))
		mock.ExpectQuery("SELECT role FROM users WHERE id = ").
			WithArgs(userID).
			WillReturnError(pgx.ErrNoRows)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, refreshRequest(refresh))

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		access := findAuthCookie(w.Result().Cookies(), auth.AccessCookieName)
		require.NotNil(t, access)
		assert.Less(t, access.MaxAge, 0)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("role lookup error", func(t *testing.T) {
		userID := uuid.New()
		refresh, err := auth.GenerateRefreshToken(userID, "user", testJWTSecret)
		require.NoError(t, err)

		mock.ExpectQuery("SELECT id, user_id, family_id, revoked_at IS NULL FROM refresh_tokens WHERE token_hash = ").
			WithArgs(auth.HashRefreshToken(refresh)).
			WillReturnRows(pgxmock.NewRows([]string{"id", "user_id", "family_id", "live"}).
				AddRow(uuid.New(), userID, uuid.New(), true))
		mock.ExpectQuery("SELECT role FROM users WHERE id = ").
			WithArgs(userID).
			WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, refreshRequest(refresh))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("session row lookup error", func(t *testing.T) {
		refresh, err := auth.GenerateRefreshToken(uuid.New(), "user", testJWTSecret)
		require.NoError(t, err)

		mock.ExpectQuery("SELECT id, user_id, family_id, revoked_at IS NULL FROM refresh_tokens WHERE token_hash = ").
			WithArgs(auth.HashRefreshToken(refresh)).
			WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, refreshRequest(refresh))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("a token this server never issued is refused", func(t *testing.T) {
		refresh, err := auth.GenerateRefreshToken(uuid.New(), "user", testJWTSecret)
		require.NoError(t, err)

		mock.ExpectQuery("SELECT id, user_id, family_id, revoked_at IS NULL FROM refresh_tokens WHERE token_hash = ").
			WithArgs(auth.HashRefreshToken(refresh)).
			WillReturnError(pgx.ErrNoRows)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, refreshRequest(refresh))

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Less(t, findAuthCookie(w.Result().Cookies(), auth.AccessCookieName).MaxAge, 0)
		assert.NotNil(t, findAuthCookie(w.Result().Cookies(), auth.RefreshCookieName))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("missing refresh cookie", func(t *testing.T) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, refreshRequest(""))

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), "missing refresh token")
	})

	t.Run("invalid refresh token clears the session", func(t *testing.T) {
		refresh, err := auth.GenerateRefreshToken(uuid.New(), "user", "wrong-secret")
		require.NoError(t, err)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, refreshRequest(refresh))

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		access := findAuthCookie(w.Result().Cookies(), auth.AccessCookieName)
		require.NotNil(t, access)
		assert.Less(t, access.MaxAge, 0)
		assert.NotNil(t, findAuthCookie(w.Result().Cookies(), auth.RefreshCookieName))
	})

	t.Run("access token cannot be used as a refresh token", func(t *testing.T) {
		access, err := auth.GenerateAccessToken(uuid.New(), "user", testJWTSecret)
		require.NoError(t, err)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, refreshRequest(access))

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

// TestRefreshDetectsReuse is the security half of finding 3: presenting a refresh
// token that has already been rotated means the value leaked (the legitimate
// holder was handed the successor), so the whole family is revoked — including
// the successor the thief would present next.
func TestRefreshDetectsReuse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	srv := newTestServer(mock)
	r := gin.New()
	r.POST("/auth/refresh", srv.Refresh)

	userID := uuid.New()
	refresh, err := auth.GenerateRefreshToken(userID, "user", testJWTSecret)
	require.NoError(t, err)
	tokenID, familyID := uuid.New(), uuid.New()

	mock.ExpectQuery("SELECT id, user_id, family_id, revoked_at IS NULL FROM refresh_tokens WHERE token_hash = ").
		WithArgs(auth.HashRefreshToken(refresh)).
		WillReturnRows(pgxmock.NewRows([]string{"id", "user_id", "family_id", "live"}).
			AddRow(tokenID, userID, familyID, false))
	// Every live token of the family is revoked, so the thief's successor dies
	// with the one he replayed.
	mock.ExpectExec("UPDATE refresh_tokens SET revoked_at = now\\(\\) WHERE family_id = ").
		WithArgs(familyID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, refreshRequest(refresh))

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	// The response is the generic expiry message: it must not tell the caller
	// that it just tripped reuse detection.
	assert.Contains(t, w.Body.String(), "session expired")
	assert.Less(t, findAuthCookie(w.Result().Cookies(), auth.AccessCookieName).MaxAge, 0)
	assert.Less(t, findAuthCookie(w.Result().Cookies(), auth.RefreshCookieName).MaxAge, 0)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A concurrent refresh of the same token (the row was live at the read, spent by
// the time the rotation update ran) is treated as a replay.
func TestRefreshDetectsConcurrentRotation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	srv := newTestServer(mock)
	r := gin.New()
	r.POST("/auth/refresh", srv.Refresh)

	userID := uuid.New()
	refresh, err := auth.GenerateRefreshToken(userID, "user", testJWTSecret)
	require.NoError(t, err)
	tokenID, familyID := uuid.New(), uuid.New()

	expectLiveRefreshSession(mock, auth.HashRefreshToken(refresh), tokenID, userID, familyID, "user")
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE refresh_tokens SET revoked_at = now\\(\\) WHERE id = ").
		WithArgs(tokenID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectRollback()
	mock.ExpectExec("UPDATE refresh_tokens SET revoked_at = now\\(\\) WHERE family_id = ").
		WithArgs(familyID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 2))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, refreshRequest(refresh))

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A successful refresh must not be blocked by an exhausted per-identity failure
// budget: the budget only ever charges failed credential checks (finding 1).
func TestRefreshDoesNotTouchTheFailureBudget(t *testing.T) {
	withAuthFailureLimiter(t, ratelimit.New(ratelimit.Config{Rate: rate.Limit(0), Burst: 1, TTL: time.Minute}))

	// Drain the identity's budget through the helper Login uses.
	c, _ := authContext("10.0.0.1")
	require.True(t, recordAuthFailure(c, "login", "user@example.com"))

	gin.SetMode(gin.TestMode)
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	srv := newTestServer(mock)
	r := gin.New()
	r.POST("/auth/refresh", srv.Refresh)

	userID := uuid.New()
	refresh, err := auth.GenerateRefreshToken(userID, "user", testJWTSecret)
	require.NoError(t, err)
	tokenID, familyID, newTokenID := uuid.New(), uuid.New(), uuid.New()

	expectLiveRefreshSession(mock, auth.HashRefreshToken(refresh), tokenID, userID, familyID, "user")
	expectRotation(mock, tokenID, userID, familyID, newTokenID)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, refreshRequest(refresh))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestAuthBodyLimit pins the request-body cap on the public credential
// endpoints: the body must be refused before it is decoded (the password rule
// runs after binding, so it cannot bound the allocation), and it must be a clean
// 413 rather than a 400.
func TestAuthBodyLimit(t *testing.T) {
	oversized := `{"email":"` + strings.Repeat("a", maxAuthBodyBytes) + `@example.com","password":"password1234"}`

	t.Run("login refuses an oversized body before decoding", func(t *testing.T) {
		// A nil pool is the point: reaching the database would panic.
		srv := newTestServer(nil)
		r := newAuthTestRouter(srv)
		r.POST("/auth/login", srv.Login)

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/auth/login", strings.NewReader(oversized))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
		assert.Contains(t, w.Body.String(), "too large")
	})

	t.Run("register refuses an oversized body before decoding", func(t *testing.T) {
		srv := newTestServer(nil)
		r := newAuthTestRouter(srv)
		r.POST("/auth/register", srv.Register)

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/auth/register", strings.NewReader(oversized))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	})

	t.Run("a body within the cap is processed normally", func(t *testing.T) {
		srv, mock := newMockServer(t)
		r := newAuthTestRouter(srv)
		r.POST("/auth/login", srv.Login)

		reqBody := models.LoginRequest{Email: "cap@example.com", Password: "password1234"}
		mock.ExpectQuery("SELECT id, email, password_hash, role FROM users").
			WithArgs(reqBody.Email).
			WillReturnRows(pgxmock.NewRows([]string{"id", "email", "password_hash", "role"}))

		jsonBody, _ := json.Marshal(reqBody)
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/auth/login", bytes.NewBuffer(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code, "an ordinary login must not hit the body cap")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// A session the server cannot record must not be handed to the browser: the
// cookie would carry a refresh token that no refresh or logout could ever
// resolve, so the request fails instead (500, no cookies).
func TestLoginWithoutStoredSessionIssuesNoCookies(t *testing.T) {
	srv, mock := newMockServer(t)
	r := newAuthTestRouter(srv)
	r.POST("/auth/login", srv.Login)

	userID := uuid.New()
	hash, err := auth.HashPassword("password1234")
	require.NoError(t, err)

	mock.ExpectQuery("SELECT id, email, password_hash, role FROM users").
		WithArgs("store@example.com").
		WillReturnRows(pgxmock.NewRows([]string{"id", "email", "password_hash", "role"}).
			AddRow(userID, "store@example.com", hash, "user"))
	mock.ExpectExec("INSERT INTO refresh_tokens").
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnError(assert.AnError)

	jsonBody, _ := json.Marshal(models.LoginRequest{Email: "store@example.com", Password: "password1234"})
	req, _ := http.NewRequest("POST", "/auth/login", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Empty(t, w.Result().Cookies(), "no session cookie for a session that was not recorded")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestLoginFailureBudget pins finding 1 end to end. The budget is charged only
// for failed credential checks, so: failures past the budget answer 429 (not a
// credential verdict), and a CORRECT password still logs in while the budget is
// drained — which is what stops an attacker from locking a chosen address out.
// A successful login refunds the budget, so the attacker cannot hold it at zero.
func TestLoginFailureBudget(t *testing.T) {
	withAuthFailureLimiter(t, ratelimit.New(ratelimit.Config{Rate: rate.Limit(0), Burst: 2, TTL: time.Minute}))

	srv, mock := newMockServer(t)
	r := newAuthTestRouter(srv)
	r.POST("/auth/login", srv.Login)

	userID := uuid.New()
	password := "password1234"
	hash, err := auth.HashPassword(password)
	require.NoError(t, err)

	attempt := func(password string, wantSession bool) *httptest.ResponseRecorder {
		t.Helper()
		mock.ExpectQuery("SELECT id, email, password_hash, role FROM users").
			WithArgs("victim@example.com").
			WillReturnRows(pgxmock.NewRows([]string{"id", "email", "password_hash", "role"}).
				AddRow(userID, "victim@example.com", hash, "user"))
		if wantSession {
			expectRefreshTokenInsert(mock)
		}

		jsonBody, _ := json.Marshal(models.LoginRequest{Email: "victim@example.com", Password: password})
		req, _ := http.NewRequest("POST", "/auth/login", bytes.NewBuffer(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	// Two wrong passwords are answered with the generic 401 and consume the
	// identity's budget.
	assert.Equal(t, http.StatusUnauthorized, attempt("wrong-password-one", false).Code)
	assert.Equal(t, http.StatusUnauthorized, attempt("wrong-password-two", false).Code)

	// The budget is spent: further failures get the throttle response instead.
	third := attempt("wrong-password-three", false)
	assert.Equal(t, http.StatusTooManyRequests, third.Code)
	assert.Equal(t, "30", third.Header().Get("Retry-After"))

	// The correct password still succeeds — the account cannot be locked out.
	// (The throttle is not consulted on success, so no 429 can preempt it, and
	// the session row insert follows the user lookup.)
	ok := attempt(password, true)
	assert.Equal(t, http.StatusOK, ok.Code)
	assert.Len(t, ok.Result().Cookies(), 2)

	// The successful login refunded the budget, so a wrong password is answered
	// with a 401 again rather than inheriting the attacker's exhausted bucket.
	assert.Equal(t, http.StatusUnauthorized, attempt("wrong-password-again", false).Code)

	assert.NoError(t, mock.ExpectationsWereMet())
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
