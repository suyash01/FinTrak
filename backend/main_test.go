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
	userToken, err := auth.GenerateToken(userID, "user", "test-secret")
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
	adminToken, err := auth.GenerateToken(userID, "admin", "test-secret")
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
