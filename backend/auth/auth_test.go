package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSecret = "unit-test-secret"

func TestHashPassword(t *testing.T) {
	hash, err := HashPassword("supersecret")
	require.NoError(t, err)
	assert.NotEmpty(t, hash)
	assert.NotEqual(t, "supersecret", hash)

	t.Run("produces different salts for same password", func(t *testing.T) {
		hash2, err := HashPassword("supersecret")
		require.NoError(t, err)
		assert.NotEqual(t, hash, hash2)
	})
}

func TestCheckPassword(t *testing.T) {
	hash, err := HashPassword("correct-password")
	require.NoError(t, err)

	assert.True(t, CheckPassword(hash, "correct-password"))
	assert.False(t, CheckPassword(hash, "wrong-password"))
	assert.False(t, CheckPassword("not-a-bcrypt-hash", "correct-password"))
}

func TestGenerateToken(t *testing.T) {
	userID := uuid.New()

	token, err := GenerateToken(userID, "admin", testSecret)
	require.NoError(t, err)
	assert.NotEmpty(t, token)
	assert.Equal(t, 3, len(strings.Split(token, ".")))

	parsed, err := jwt.ParseWithClaims(token, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, assert.AnError
		}
		return []byte(testSecret), nil
	})
	require.NoError(t, err)
	assert.True(t, parsed.Valid)

	claims, ok := parsed.Claims.(*Claims)
	require.True(t, ok)
	assert.Equal(t, userID, claims.UserID)
	assert.Equal(t, "admin", claims.Role)
	assert.NotNil(t, claims.ExpiresAt)
	assert.NotNil(t, claims.IssuedAt)
	assert.True(t, claims.ExpiresAt.After(claims.IssuedAt.Time))
}

func TestRequireAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newRouter := func() *gin.Engine {
		r := gin.New()
		r.GET("/protected", RequireAuth(testSecret), func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"userID": GetUserID(c)})
		})
		return r
	}

	t.Run("missing header", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		newRouter().ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), "missing authorization header")
	})

	t.Run("invalid header scheme", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.Header.Set("Authorization", "Basic abc123")
		newRouter().ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), "invalid authorization header")
	})

	t.Run("valid token", func(t *testing.T) {
		userID := uuid.New()
		token, err := GenerateToken(userID, "user", testSecret)
		require.NoError(t, err)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		newRouter().ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), userID.String())
	})

	t.Run("token signed with wrong secret", func(t *testing.T) {
		otherToken, err := GenerateToken(uuid.New(), "user", "some-other-secret")
		require.NoError(t, err)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.Header.Set("Authorization", "Bearer "+otherToken)
		newRouter().ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), "invalid or expired token")
	})

	t.Run("malformed token", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.Header.Set("Authorization", "Bearer not-a-jwt")
		newRouter().ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

func TestGetUserID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("returns nil when not set", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		assert.Equal(t, uuid.Nil, GetUserID(c))
	})

	t.Run("returns set user id", func(t *testing.T) {
		userID := uuid.New()
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Set(ctxUserIDKey, userID)
		assert.Equal(t, userID, GetUserID(c))
	})
}

func TestRequireAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newRouter := func() *gin.Engine {
		r := gin.New()
		r.GET("/admin", RequireAuth(testSecret), RequireAdmin(), func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"role": GetUserRole(c)})
		})
		return r
	}

	t.Run("admin token allowed", func(t *testing.T) {
		token, err := GenerateToken(uuid.New(), "admin", testSecret)
		require.NoError(t, err)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/admin", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		newRouter().ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `"role":"admin"`)
	})

	t.Run("user token forbidden", func(t *testing.T) {
		token, err := GenerateToken(uuid.New(), "user", testSecret)
		require.NoError(t, err)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/admin", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		newRouter().ServeHTTP(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Contains(t, w.Body.String(), "admin access required")
	})

	t.Run("missing role forbidden", func(t *testing.T) {
		token, err := GenerateToken(uuid.New(), "", testSecret)
		require.NoError(t, err)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/admin", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		newRouter().ServeHTTP(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})
}

func TestGetUserRole(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("returns empty when not set", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		assert.Equal(t, "", GetUserRole(c))
	})

	t.Run("returns set role", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Set(ctxUserRoleKey, "admin")
		assert.Equal(t, "admin", GetUserRole(c))
	})
}
