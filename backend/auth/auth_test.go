package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	assert.Equal(t, tokenIssuer, claims.Issuer)
	assert.Equal(t, jwt.ClaimStrings{tokenAudience}, claims.Audience)
	assert.Equal(t, userID.String(), claims.Subject)
	assert.NotEmpty(t, claims.ID)
	assert.WithinDuration(t, time.Now().Add(tokenTTL), claims.ExpiresAt.Time, time.Minute)
	require.NotNil(t, claims.SessionExpiresAt)
	assert.WithinDuration(t, time.Now().Add(sessionTTL), claims.SessionExpiresAt.Time, time.Minute)
}

// signClaims mints an HS256 token from arbitrary claims, letting tests craft
// near-expiry and expired-session tokens that GenerateToken cannot produce.
func signClaims(t *testing.T, claims Claims, secret string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(secret))
	require.NoError(t, err)
	return signed
}

func TestRequireAuthEnforcesSessionDeadline(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.GET("/protected", RequireAuth(testSecret), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"userID": GetUserID(c)})
	})

	now := time.Now()
	token := signClaims(t, Claims{
		UserID:           uuid.New(),
		Role:             "user",
		SessionExpiresAt: jwt.NewNumericDate(now.Add(-time.Minute)),
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tokenIssuer,
			Subject:   uuid.NewString(),
			Audience:  jwt.ClaimStrings{tokenAudience},
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}, testSecret)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "session expired")
}

func TestRenewSession(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newRouter := func() *gin.Engine {
		r := gin.New()
		r.GET("/protected", RequireAuth(testSecret), RenewSession(testSecret, false), func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"userID": GetUserID(c)})
		})
		return r
	}

	do := func(claims Claims) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.Header.Set("Authorization", "Bearer "+signClaims(t, claims, testSecret))
		newRouter().ServeHTTP(w, req)
		return w
	}

	baseClaims := func(expiresIn, sessionIn time.Duration) Claims {
		now := time.Now()
		return Claims{
			UserID:           uuid.New(),
			Role:             "user",
			SessionExpiresAt: jwt.NewNumericDate(now.Add(sessionIn)),
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    tokenIssuer,
				Audience:  jwt.ClaimStrings{tokenAudience},
				ExpiresAt: jwt.NewNumericDate(now.Add(expiresIn)),
				IssuedAt:  jwt.NewNumericDate(now),
			},
		}
	}

	t.Run("renews a token near expiry", func(t *testing.T) {
		userID := uuid.New()
		claims := baseClaims(10*time.Minute, 10*24*time.Hour)
		claims.UserID = userID

		w := do(claims)
		require.Equal(t, http.StatusOK, w.Code)

		cookies := w.Result().Cookies()
		require.Len(t, cookies, 1)
		assert.Equal(t, AuthCookieName, cookies[0].Name)

		// The replacement must be parseable and preserve the user and the
		// absolute session deadline.
		parsed, err := jwt.ParseWithClaims(cookies[0].Value, &Claims{}, func(*jwt.Token) (any, error) {
			return []byte(testSecret), nil
		})
		require.NoError(t, err)
		renewed, ok := parsed.Claims.(*Claims)
		require.True(t, ok)
		assert.Equal(t, userID, renewed.UserID)
		require.NotNil(t, renewed.SessionExpiresAt)
		assert.WithinDuration(t, claims.SessionExpiresAt.Time, renewed.SessionExpiresAt.Time, time.Second)
	})

	t.Run("leaves a fresh token alone", func(t *testing.T) {
		w := do(baseClaims(time.Hour, 10*24*time.Hour))
		require.Equal(t, http.StatusOK, w.Code)
		assert.Empty(t, w.Result().Cookies())
	})

	t.Run("does not renew past the session deadline", func(t *testing.T) {
		// Token expires in 10m, but the whole session has only 10m left: the
		// absolute cap wins and no replacement is issued.
		w := do(baseClaims(10*time.Minute, 10*time.Minute))
		require.Equal(t, http.StatusOK, w.Code)
		assert.Empty(t, w.Result().Cookies())
	})

	t.Run("legacy token without session deadline is not renewed", func(t *testing.T) {
		now := time.Now()
		token := signClaims(t, Claims{
			UserID: uuid.New(),
			Role:   "user",
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    tokenIssuer,
				Audience:  jwt.ClaimStrings{tokenAudience},
				ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
				IssuedAt:  jwt.NewNumericDate(now),
			},
		}, testSecret)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		newRouter().ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		assert.Empty(t, w.Result().Cookies())
	})
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
		assert.Contains(t, w.Body.String(), "missing authentication")
	})

	t.Run("invalid header scheme", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.Header.Set("Authorization", "Basic abc123")
		newRouter().ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), "missing authentication")
	})

	t.Run("valid session cookie", func(t *testing.T) {
		userID := uuid.New()
		token, err := GenerateToken(userID, "user", testSecret)
		require.NoError(t, err)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.AddCookie(&http.Cookie{Name: AuthCookieName, Value: token})
		newRouter().ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), userID.String())
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

	sign := func(issuer string, audience jwt.ClaimStrings) string {
		now := time.Now()
		claims := Claims{
			UserID: uuid.New(),
			Role:   "user",
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    issuer,
				Audience:  audience,
				ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
				IssuedAt:  jwt.NewNumericDate(now),
			},
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		signed, err := tok.SignedString([]byte(testSecret))
		require.NoError(t, err)
		return signed
	}

	t.Run("wrong issuer", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.Header.Set("Authorization", "Bearer "+sign("not-fintrak", jwt.ClaimStrings{tokenAudience}))
		newRouter().ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("wrong audience", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.Header.Set("Authorization", "Bearer "+sign(tokenIssuer, jwt.ClaimStrings{"other-api"}))
		newRouter().ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("malformed token", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.Header.Set("Authorization", "Bearer not-a-jwt")
		newRouter().ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

func TestSetAndClearAuthCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newCtx := func() (*gin.Context, *httptest.ResponseRecorder) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		return c, w
	}

	t.Run("production sets Secure and HttpOnly", func(t *testing.T) {
		c, w := newCtx()
		SetAuthCookie(c, "tok", true)

		cookies := w.Result().Cookies()
		require.Len(t, cookies, 1)
		assert.Equal(t, AuthCookieName, cookies[0].Name)
		assert.True(t, cookies[0].HttpOnly)
		assert.True(t, cookies[0].Secure)
		assert.Equal(t, http.SameSiteLaxMode, cookies[0].SameSite)
		assert.NotEmpty(t, cookies[0].Value)
	})

	t.Run("development omits Secure", func(t *testing.T) {
		c, w := newCtx()
		SetAuthCookie(c, "tok", false)

		cookies := w.Result().Cookies()
		require.Len(t, cookies, 1)
		assert.False(t, cookies[0].Secure)
	})

	t.Run("clear expires the cookie", func(t *testing.T) {
		c, w := newCtx()
		ClearAuthCookie(c, true)

		cookies := w.Result().Cookies()
		require.Len(t, cookies, 1)
		assert.Equal(t, "", cookies[0].Value)
		assert.Less(t, cookies[0].MaxAge, 0)
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
