package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	return setupRouter(&config.Config{
		DatabaseURL:    "postgres://test",
		Port:           "8080",
		AllowedOrigins: []string{"http://localhost:5173", "http://127.0.0.1:5173", "*"},
		JWTSecret:      "test-secret",
	})
}

func TestHealthEndpoint(t *testing.T) {
	r := testRouter()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"status":"ok"}`, w.Body.String())
}

func TestProtectedRoutesRequireAuth(t *testing.T) {
	r := testRouter()

	paths := []string{
		"/api/v1/accounts",
		"/api/v1/account-types",
		"/api/v1/categories",
		"/api/v1/transactions",
		"/api/v1/rules",
		"/api/v1/payees",
		"/api/v1/links",
		"/api/v1/dashboard/summary",
	}

	for _, path := range paths {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code, "expected 401 for %s", path)
	}
}

func TestAccountTypeMutationsRequireAdmin(t *testing.T) {
	r := testRouter()

	userID := uuid.New()
	userToken, err := auth.GenerateAccessToken(userID, "user", "test-secret")
	require.NoError(t, err)

	// No auth -> 401.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/account-types", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// Authenticated non-admin -> 403.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/account-types", nil)
	req.Header.Set("Authorization", "Bearer "+userToken)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)

	// Admin token passes the middleware and reaches the handler (missing body
	// fields -> binding error 400, no DB touched).
	adminToken, err := auth.GenerateAccessToken(userID, "admin", "test-secret")
	require.NoError(t, err)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/account-types", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCORSAllowsConfiguredOrigin(t *testing.T) {
	r := testRouter()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "http://localhost:5173", w.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORSCoveredByWildcard(t *testing.T) {
	r := testRouter()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("Origin", "https://anything.example.com")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "https://anything.example.com", w.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORSRejectsUnknownOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := setupRouter(&config.Config{
		DatabaseURL:    "postgres://test",
		Port:           "8080",
		AllowedOrigins: []string{"http://localhost:5173"},
		JWTSecret:      "test-secret",
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORSWildcardDisablesCredentials(t *testing.T) {
	r := testRouter() // testRouter configures a "*" origin

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("Origin", "https://anything.example.com")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Credentials"))
}

func TestCORSConfiguredOriginAllowsCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := setupRouter(&config.Config{
		DatabaseURL:    "postgres://test",
		Port:           "8080",
		AllowedOrigins: []string{"http://localhost:5173"},
		JWTSecret:      "test-secret",
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "true", w.Header().Get("Access-Control-Allow-Credentials"))
}

func TestSecurityHeaders(t *testing.T) {
	r := testRouter()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
	assert.Equal(t, "no-referrer", w.Header().Get("Referrer-Policy"))
	assert.NotEmpty(t, w.Header().Get("Content-Security-Policy"))
}

func TestNewServerTimeouts(t *testing.T) {
	srv := newServer(":0", http.NewServeMux())

	assert.Equal(t, readHeaderTimeout, srv.ReadHeaderTimeout)
	assert.Equal(t, readTimeout, srv.ReadTimeout)
	assert.Equal(t, writeTimeout, srv.WriteTimeout)
	assert.Equal(t, idleTimeout, srv.IdleTimeout)
}

// TestCrossSiteGetGuard pins the backstop for the GET routes that write: a
// SameSite=Lax cookie IS attached to a cross-site top-level navigation, so a
// browser can be steered into a blind, cookie-authenticated write through these
// routes. Only an explicit `cross-site` claim is refused — a same-origin SPA/PWA
// request and a non-browser client (which sends no Sec-Fetch-* header at all)
// must both pass.
func TestCrossSiteGetGuard(t *testing.T) {
	const token = "00000000-0000-0000-0000-000000000001"

	writePaths := []string{
		"/api/v1/accounts/" + token + "/billing-cycles",
		"/api/v1/accounts/" + token + "/export",
		"/api/v1/transactions",
		"/api/v1/dashboard/summary",
		"/api/v1/dashboard/money-flow",
		"/api/v1/dashboard/money-flow/timeline",
		"/api/v1/dashboard/cash-flow-calendar",
		"/api/v1/paperless/documents",
	}

	t.Run("cross-site is refused before authentication", func(t *testing.T) {
		r := testRouter()
		for _, path := range writePaths {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Sec-Fetch-Site", "cross-site")
			r.ServeHTTP(w, req)

			assert.Equal(t, http.StatusForbidden, w.Code, "expected a 403 for cross-site GET %s", path)
			assert.Contains(t, w.Body.String(), "cross-site")
		}
	})

	t.Run("same-origin reaches the handler chain", func(t *testing.T) {
		r := testRouter()
		for _, path := range writePaths {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			r.ServeHTTP(w, req)

			// No session cookie here, so the request is stopped by RequireAuth —
			// what matters is that the guard did not refuse it.
			assert.Equal(t, http.StatusUnauthorized, w.Code, "same-origin GET %s must not be blocked as cross-site", path)
		}
	})

	t.Run("an absent header is allowed", func(t *testing.T) {
		r := testRouter()
		for _, path := range writePaths {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			r.ServeHTTP(w, req)

			assert.Equal(t, http.StatusUnauthorized, w.Code, "a non-browser client GET %s must not be blocked", path)
		}
	})

	t.Run("a sibling site is allowed", func(t *testing.T) {
		r := testRouter()

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/summary", nil)
		req.Header.Set("Sec-Fetch-Site", "same-site")
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("only the state-changing routes are covered", func(t *testing.T) {
		r := testRouter()

		// A pure read stays reachable cross-site: the guard must not break
		// unrelated cross-site reads.
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)

		w = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		// Non-GET requests are not this guard's business: SameSite=Lax already
		// keeps the cookies off a cross-site POST.
		w = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPost, "/api/v1/transactions", nil)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

func TestRouterRegistersExpectedRoutes(t *testing.T) {
	r := testRouter()

	registered := make(map[string]string)
	for _, route := range r.Routes() {
		registered[route.Method+" "+route.Path] = route.Handler
	}

	expected := []string{
		"GET /api/v1/health",
		"POST /api/v1/auth/register",
		"POST /api/v1/auth/login",
		"POST /api/v1/auth/refresh",
		"POST /api/v1/auth/logout",
		"GET /api/v1/accounts",
		"GET /api/v1/account-types",
		"GET /api/v1/categories",
		"POST /api/v1/categories",
		"PUT /api/v1/categories/:id",
		"DELETE /api/v1/categories/:id",
		"GET /api/v1/groups",
		"POST /api/v1/groups",
		"PUT /api/v1/groups/:id",
		"DELETE /api/v1/groups/:id",
		"POST /api/v1/admin/groups",
		"POST /api/v1/admin/categories",
		"PUT /api/v1/admin/categories/:id",
		"DELETE /api/v1/admin/categories/:id",
		"GET /api/v1/transactions",
		"GET /api/v1/rules",
		"GET /api/v1/payees",
		"GET /api/v1/links",
		"GET /api/v1/dashboard/summary",
		"POST /api/v1/statements/parse",
		"GET /api/v1/statements/extractors",
	}

	for _, key := range expected {
		_, ok := registered[key]
		require.True(t, ok, "missing route %s", key)
	}
}
