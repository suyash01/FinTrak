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

func TestGenerateAccessToken(t *testing.T) {
	userID := uuid.New()

	token, err := GenerateAccessToken(userID, "admin", testSecret)
	require.NoError(t, err)
	assert.NotEmpty(t, token)
	assert.Equal(t, 3, len(strings.Split(token, ".")))

	claims, err := ParseToken(token, testSecret, TokenTypeAccess)
	require.NoError(t, err)
	assert.Equal(t, userID, claims.UserID)
	assert.Equal(t, "admin", claims.Role)
	assert.Equal(t, TokenTypeAccess, claims.TokenType)
	require.NotNil(t, claims.ExpiresAt)
	assert.NotNil(t, claims.IssuedAt)
	assert.True(t, claims.ExpiresAt.After(claims.IssuedAt.Time))
	assert.Equal(t, tokenIssuer, claims.Issuer)
	assert.Equal(t, jwt.ClaimStrings{tokenAudience}, claims.Audience)
	assert.Equal(t, userID.String(), claims.Subject)
	assert.NotEmpty(t, claims.ID)
	assert.WithinDuration(t, time.Now().Add(accessTokenTTL), claims.ExpiresAt.Time, time.Minute)
}

func TestGenerateRefreshToken(t *testing.T) {
	userID := uuid.New()

	token, err := GenerateRefreshToken(userID, "user", testSecret)
	require.NoError(t, err)

	claims, err := ParseToken(token, testSecret, TokenTypeRefresh)
	require.NoError(t, err)
	assert.Equal(t, userID, claims.UserID)
	assert.Equal(t, "user", claims.Role)
	assert.Equal(t, TokenTypeRefresh, claims.TokenType)
	require.NotNil(t, claims.ExpiresAt)
	assert.WithinDuration(t, time.Now().Add(refreshTokenTTL), claims.ExpiresAt.Time, time.Minute)
}

func TestNewSession(t *testing.T) {
	userID := uuid.New()

	session, err := NewSession(userID, "admin", testSecret)
	require.NoError(t, err)

	accessClaims, err := ParseToken(session.AccessToken, testSecret, TokenTypeAccess)
	require.NoError(t, err)
	assert.Equal(t, userID, accessClaims.UserID)
	assert.Equal(t, "admin", accessClaims.Role)

	refreshClaims, err := ParseToken(session.RefreshToken, testSecret, TokenTypeRefresh)
	require.NoError(t, err)
	assert.Equal(t, userID, refreshClaims.UserID)
	assert.Equal(t, "admin", refreshClaims.Role)
	assert.NotEqual(t, session.AccessToken, session.RefreshToken)

	// ExpiresAt is the session's absolute deadline, and it is exactly the expiry
	// the refresh token carries: the handler stores it next to the token's hash
	// so a rotated session can never outlive the login.
	require.NotNil(t, refreshClaims.ExpiresAt)
	assert.Equal(t, refreshClaims.ExpiresAt.Time.Unix(), session.ExpiresAt.Unix())
	assert.WithinDuration(t, time.Now().Add(refreshTokenTTL), session.ExpiresAt, time.Minute)
}

func TestRenewRefresh(t *testing.T) {
	userID := uuid.New()

	refresh, err := GenerateRefreshToken(userID, "user", testSecret)
	require.NoError(t, err)
	claims, err := ParseToken(refresh, testSecret, TokenTypeRefresh)
	require.NoError(t, err)

	t.Run("rotation keeps the session's absolute deadline", func(t *testing.T) {
		rotated, err := RenewRefresh(claims, "user", testSecret)
		require.NoError(t, err)
		assert.NotEqual(t, refresh, rotated)

		rotatedClaims, err := ParseToken(rotated, testSecret, TokenTypeRefresh)
		require.NoError(t, err)
		assert.Equal(t, userID, rotatedClaims.UserID)
		assert.Equal(t, TokenTypeRefresh, rotatedClaims.TokenType)
		require.NotNil(t, rotatedClaims.ExpiresAt)
		assert.WithinDuration(t, claims.ExpiresAt.Time, rotatedClaims.ExpiresAt.Time, time.Second)
	})

	t.Run("mints the role the caller resolved, not the token's copy", func(t *testing.T) {
		rotated, err := RenewRefresh(claims, "admin", testSecret)
		require.NoError(t, err)
		rotatedClaims, err := ParseToken(rotated, testSecret, TokenTypeRefresh)
		require.NoError(t, err)
		assert.Equal(t, "admin", rotatedClaims.Role)
	})

	t.Run("refuses a session past its deadline", func(t *testing.T) {
		expired := &Claims{UserID: userID, ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute))}
		_, err := RenewRefresh(expired, "user", testSecret)
		require.Error(t, err)
	})

	t.Run("refuses claims without an expiry", func(t *testing.T) {
		_, err := RenewRefresh(&Claims{UserID: userID}, "user", testSecret)
		require.Error(t, err)
		_, err = RenewRefresh(nil, "user", testSecret)
		require.Error(t, err)
	})
}

func TestHashRefreshToken(t *testing.T) {
	hash := HashRefreshToken("some.refresh.token")

	// SHA-256 in hex: 64 characters, stable, and never the token itself.
	assert.Len(t, hash, 64)
	assert.Equal(t, hash, HashRefreshToken("some.refresh.token"))
	assert.NotEqual(t, hash, HashRefreshToken("some.refresh.tokeN"))
	assert.NotContains(t, hash, "some.refresh.token")
}

func TestRenewAccess(t *testing.T) {
	userID := uuid.New()

	t.Run("mints a new access token for the same identity", func(t *testing.T) {
		refresh, err := GenerateRefreshToken(userID, "user", testSecret)
		require.NoError(t, err)
		claims, err := ParseToken(refresh, testSecret, TokenTypeRefresh)
		require.NoError(t, err)

		access, err := RenewAccess(claims, "user", testSecret)
		require.NoError(t, err)

		renewed, err := ParseToken(access, testSecret, TokenTypeAccess)
		require.NoError(t, err)
		assert.Equal(t, userID, renewed.UserID)
		assert.Equal(t, "user", renewed.Role)
	})

	t.Run("mints the role the caller resolved, not the token's copy", func(t *testing.T) {
		// The handler reads the role from the database, so a demotion has to
		// reach the new access token instead of the stale claim.
		refresh, err := GenerateRefreshToken(userID, "admin", testSecret)
		require.NoError(t, err)
		claims, err := ParseToken(refresh, testSecret, TokenTypeRefresh)
		require.NoError(t, err)

		access, err := RenewAccess(claims, "user", testSecret)
		require.NoError(t, err)

		renewed, err := ParseToken(access, testSecret, TokenTypeAccess)
		require.NoError(t, err)
		assert.Equal(t, "user", renewed.Role)
	})

	t.Run("caps the access token at the session deadline", func(t *testing.T) {
		// A refresh token with only five minutes of session left must never
		// yield a 15-minute access token.
		now := time.Now()
		claims := &Claims{
			UserID:    userID,
			Role:      "user",
			TokenType: TokenTypeRefresh,
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    tokenIssuer,
				Audience:  jwt.ClaimStrings{tokenAudience},
				ExpiresAt: jwt.NewNumericDate(now.Add(5 * time.Minute)),
				IssuedAt:  jwt.NewNumericDate(now),
			},
		}

		access, err := RenewAccess(claims, "user", testSecret)
		require.NoError(t, err)
		renewed, err := ParseToken(access, testSecret, TokenTypeAccess)
		require.NoError(t, err)
		assert.WithinDuration(t, claims.ExpiresAt.Time, renewed.ExpiresAt.Time, time.Second)
	})

	t.Run("refuses an already-expired session", func(t *testing.T) {
		now := time.Now()
		claims := &Claims{
			UserID:    userID,
			Role:      "user",
			TokenType: TokenTypeRefresh,
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(now.Add(-time.Minute)),
			},
		}

		_, err := RenewAccess(claims, "user", testSecret)
		assert.Error(t, err)
	})

	t.Run("refuses claims without an expiry", func(t *testing.T) {
		_, err := RenewAccess(&Claims{UserID: userID}, "user", testSecret)
		assert.Error(t, err)
		_, err = RenewAccess(nil, "user", testSecret)
		assert.Error(t, err)
	})
}

// signClaims mints an HS256 token from arbitrary claims, letting tests craft
// tokens with unusual types or expiries that the generators cannot produce.
func signClaims(t *testing.T, claims Claims, secret string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(secret))
	require.NoError(t, err)
	return signed
}

func TestParseTokenRejectsWrongType(t *testing.T) {
	userID := uuid.New()

	access, err := GenerateAccessToken(userID, "user", testSecret)
	require.NoError(t, err)
	refresh, err := GenerateRefreshToken(userID, "user", testSecret)
	require.NoError(t, err)

	t.Run("access token cannot be used as a refresh token", func(t *testing.T) {
		_, err := ParseToken(access, testSecret, TokenTypeRefresh)
		assert.Error(t, err)
	})

	t.Run("refresh token cannot be used as an access token", func(t *testing.T) {
		_, err := ParseToken(refresh, testSecret, TokenTypeAccess)
		assert.Error(t, err)
	})

	t.Run("empty type cannot be used", func(t *testing.T) {
		token := signClaims(t, Claims{
			UserID: userID,
			Role:   "user",
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    tokenIssuer,
				Audience:  jwt.ClaimStrings{tokenAudience},
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
				IssuedAt:  jwt.NewNumericDate(time.Now()),
			},
		}, testSecret)
		_, err := ParseToken(token, testSecret, TokenTypeAccess)
		assert.Error(t, err)
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
		token, err := GenerateAccessToken(userID, "user", testSecret)
		require.NoError(t, err)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.AddCookie(&http.Cookie{Name: AccessCookieName, Value: token})
		newRouter().ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), userID.String())
	})

	t.Run("valid token", func(t *testing.T) {
		userID := uuid.New()
		token, err := GenerateAccessToken(userID, "user", testSecret)
		require.NoError(t, err)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		newRouter().ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), userID.String())
	})

	t.Run("refresh token is rejected", func(t *testing.T) {
		token, err := GenerateRefreshToken(uuid.New(), "user", testSecret)
		require.NoError(t, err)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		newRouter().ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("token signed with wrong secret", func(t *testing.T) {
		otherToken, err := GenerateAccessToken(uuid.New(), "user", "some-other-secret")
		require.NoError(t, err)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.Header.Set("Authorization", "Bearer "+otherToken)
		newRouter().ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), "invalid or expired token")
	})

	sign := func(issuer string, audience jwt.ClaimStrings) string {
		return signClaims(t, Claims{
			UserID:    uuid.New(),
			Role:      "user",
			TokenType: TokenTypeAccess,
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    issuer,
				Audience:  audience,
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
				IssuedAt:  jwt.NewNumericDate(time.Now()),
			},
		}, testSecret)
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

func TestSetAndClearAuthCookies(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newCtx := func() (*gin.Context, *httptest.ResponseRecorder) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		return c, w
	}

	cookieByName := func(cookies []*http.Cookie, name string) *http.Cookie {
		for _, ck := range cookies {
			if ck.Name == name {
				return ck
			}
		}
		return nil
	}

	t.Run("production sets Secure and HttpOnly on both cookies", func(t *testing.T) {
		c, w := newCtx()
		SetAuthCookies(c, "access", "refresh", true)

		cookies := w.Result().Cookies()
		require.Len(t, cookies, 2)

		access := cookieByName(cookies, AccessCookieName)
		require.NotNil(t, access)
		assert.Equal(t, "access", access.Value)
		assert.True(t, access.HttpOnly)
		assert.True(t, access.Secure)
		assert.Equal(t, http.SameSiteLaxMode, access.SameSite)
		assert.Equal(t, "/", access.Path)

		refresh := cookieByName(cookies, RefreshCookieName)
		require.NotNil(t, refresh)
		assert.Equal(t, "refresh", refresh.Value)
		assert.True(t, refresh.HttpOnly)
		assert.True(t, refresh.Secure)
		assert.Equal(t, http.SameSiteLaxMode, refresh.SameSite)
		assert.Equal(t, RefreshCookiePath, refresh.Path)
	})

	t.Run("development omits Secure", func(t *testing.T) {
		c, w := newCtx()
		SetAuthCookies(c, "access", "refresh", false)

		for _, ck := range w.Result().Cookies() {
			assert.False(t, ck.Secure)
		}
	})

	t.Run("clear expires both cookies", func(t *testing.T) {
		c, w := newCtx()
		ClearAuthCookies(c, true)

		cookies := w.Result().Cookies()
		require.Len(t, cookies, 2)
		for _, ck := range cookies {
			assert.Equal(t, "", ck.Value)
			assert.Less(t, ck.MaxAge, 0)
		}
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
		token, err := GenerateAccessToken(uuid.New(), "admin", testSecret)
		require.NoError(t, err)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/admin", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		newRouter().ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `"role":"admin"`)
	})

	t.Run("user token forbidden", func(t *testing.T) {
		token, err := GenerateAccessToken(uuid.New(), "user", testSecret)
		require.NoError(t, err)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/admin", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		newRouter().ServeHTTP(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Contains(t, w.Body.String(), "admin access required")
	})

	t.Run("missing role forbidden", func(t *testing.T) {
		token, err := GenerateAccessToken(uuid.New(), "", testSecret)
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
