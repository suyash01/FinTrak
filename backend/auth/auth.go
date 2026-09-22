// Package auth provides password hashing, JWT issuance/validation, and the Gin
// middleware that protects authenticated routes. Authenticated user IDs and
// roles are stashed in the request context for handlers to consume.
package auth

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/fintrak/backend/internal/validation"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

const (
	ctxUserIDKey   = "userID"
	ctxUserRoleKey = "userRole"
	ctxClaimsKey   = "authClaims"

	// accessTokenTTL is how long an issued access token stays valid. It is
	// deliberately short: the refresh token is what keeps a session alive, and
	// the frontend transparently trades a 401 for a new access token via
	// POST /auth/refresh.
	accessTokenTTL = 15 * time.Minute
	// refreshTokenTTL is the absolute lifetime of a session, measured from the
	// initial login. Refreshing mints a new access token but never extends a
	// session past this deadline, so a user logs in again every 30 days.
	refreshTokenTTL = 30 * 24 * time.Hour

	// tokenIssuer and tokenAudience are validated on every request so tokens
	// minted for another service (or with the same secret but different intent)
	// are rejected.
	tokenIssuer   = "fintrak"
	tokenAudience = "fintrak-api"
)

// Token type values embedded in Claims.TokenType. They stop an access token
// from being replayed against the refresh endpoint (or a refresh token against
// a protected route).
const (
	TokenTypeAccess  = "access"
	TokenTypeRefresh = "refresh"
)

// Claims is the JWT payload for FinTrak tokens: the user ID, role, the token
// type, and standard registered claims. Access and refresh tokens share this
// shape; TokenType distinguishes them.
type Claims struct {
	UserID    uuid.UUID `json:"user_id"`
	Role      string    `json:"role"`
	TokenType string    `json:"token_type"`
	jwt.RegisteredClaims
}

// HashPassword returns a bcrypt hash of the given password.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}

// CheckPassword reports whether the plaintext password matches the bcrypt hash.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// dummyPasswordHash is a valid bcrypt hash of a value no account can hold. It
// exists so a login for an unknown email can spend the same work as one with a
// wrong password.
const dummyPasswordHash = "$2a$10$5NnbSCRv/rl77g0wE7LLfOfnt/KUn4mwoOEZau5p4Or5lca4aNGV."

// EqualizePasswordTiming performs the bcrypt comparison a wrong-password login
// performs, for callers that returned early because the account does not exist.
// Without it the response time tells an unknown email (~1 ms) from a known one
// (~60-100 ms at the default cost) even though both answer the identical 401,
// which enumerates accounts.
func EqualizePasswordTiming(password string) {
	_ = bcrypt.CompareHashAndPassword([]byte(dummyPasswordHash), []byte(password))
}

// NewSession mints a fresh access/refresh token pair for a new login. The
// refresh token's expiry is the session's absolute deadline.
func NewSession(userID uuid.UUID, role, secret string) (accessToken, refreshToken string, err error) {
	if accessToken, err = GenerateAccessToken(userID, role, secret); err != nil {
		return "", "", err
	}
	if refreshToken, err = GenerateRefreshToken(userID, role, secret); err != nil {
		return "", "", err
	}
	return accessToken, refreshToken, nil
}

// GenerateAccessToken signs a short-lived HS256 access token. It is sent with
// every API request and is the only token RequireAuth accepts.
func GenerateAccessToken(userID uuid.UUID, role, secret string) (string, error) {
	return generateToken(userID, role, secret, TokenTypeAccess, accessTokenTTL, time.Time{})
}

// GenerateRefreshToken signs a long-lived HS256 refresh token, starting a new
// session that ends refreshTokenTTL from now. It is only ever sent to
// POST /auth/refresh, which trades it for a fresh access token.
func GenerateRefreshToken(userID uuid.UUID, role, secret string) (string, error) {
	return generateToken(userID, role, secret, TokenTypeRefresh, refreshTokenTTL, time.Time{})
}

// RenewAccess mints a new access token bounded by the refresh token's expiry,
// so refreshing near the end of a session can never produce an access token
// that outlives the session's absolute deadline. The role comes from the caller
// (which reads it from the database) rather than from the refresh token: the
// token carries the role it was minted with, so re-using it would keep a
// demoted admin's access alive for the token's whole 30-day life.
func RenewAccess(claims *Claims, role, secret string) (string, error) {
	if claims == nil || claims.ExpiresAt == nil {
		return "", errors.New("refresh token has no expiry")
	}
	return generateToken(claims.UserID, role, secret, TokenTypeAccess, accessTokenTTL, claims.ExpiresAt.Time)
}

// generateToken signs a token of the given type. Its expiry is now+ttl, capped
// at notAfter when that is non-zero.
func generateToken(userID uuid.UUID, role, secret, tokenType string, ttl time.Duration, notAfter time.Time) (string, error) {
	now := time.Now()
	expiresAt := now.Add(ttl)
	if !notAfter.IsZero() && notAfter.Before(expiresAt) {
		expiresAt = notAfter
	}
	if !expiresAt.After(now) {
		return "", errors.New("token expiry is in the past")
	}

	claims := Claims{
		UserID:    userID,
		Role:      role,
		TokenType: tokenType,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tokenIssuer,
			Subject:   userID.String(),
			Audience:  jwt.ClaimStrings{tokenAudience},
			ID:        uuid.NewString(),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// ParseToken validates a signed token's signature, issuer, audience, and
// expiry, and requires its type claim to equal wantType. It is used both by
// RequireAuth (wantType=access) and the refresh endpoint (wantType=refresh).
func ParseToken(tokenString, secret, wantType string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(secret), nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(tokenIssuer),
		jwt.WithAudience(tokenAudience),
	)
	if err != nil || !token.Valid {
		return nil, errors.New("invalid or expired token")
	}

	claims, ok := token.Claims.(*Claims)
	if !ok {
		return nil, errors.New("invalid token claims")
	}
	if claims.TokenType != wantType {
		return nil, errors.New("unexpected token type")
	}
	return claims, nil
}

const (
	// AccessCookieName is the httpOnly cookie that carries the short-lived
	// access token. It is sent with every API request.
	AccessCookieName = "fintrak_token"
	// RefreshCookieName is the httpOnly cookie that carries the long-lived
	// refresh token. It is scoped to the auth endpoints so it is only ever
	// transmitted to /auth/refresh and /auth/logout.
	RefreshCookieName = "fintrak_refresh"
	// RefreshCookiePath scopes the refresh cookie to the auth endpoints.
	RefreshCookiePath = "/api/v1/auth"
)

// SetAuthCookies writes the access and refresh tokens as httpOnly, SameSite=Lax
// cookies so neither is readable from JavaScript. Pass secure=true in
// production (HTTPS). SameSite=Lax is the CSRF defense: browsers do not attach
// the cookies to cross-site POST/PUT/PATCH/DELETE requests.
func SetAuthCookies(c *gin.Context, accessToken, refreshToken string, secure bool) {
	setCookie(c, AccessCookieName, accessToken, "/", int(accessTokenTTL.Seconds()), secure)
	setCookie(c, RefreshCookieName, refreshToken, RefreshCookiePath, int(refreshTokenTTL.Seconds()), secure)
}

// SetAccessCookie refreshes just the access-token cookie. Used by the refresh
// endpoint so the long-lived refresh cookie keeps its original expiry and the
// session's absolute deadline is preserved.
func SetAccessCookie(c *gin.Context, token string, secure bool) {
	setCookie(c, AccessCookieName, token, "/", int(accessTokenTTL.Seconds()), secure)
}

// ClearAuthCookies expires both session cookies.
func ClearAuthCookies(c *gin.Context, secure bool) {
	setCookie(c, AccessCookieName, "", "/", -1, secure)
	setCookie(c, RefreshCookieName, "", RefreshCookiePath, -1, secure)
}

func setCookie(c *gin.Context, name, value, path string, maxAge int, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// bearerToken extracts the token from an `Authorization: Bearer <token>`
// header, returning "" when the header is absent or uses another scheme.
func bearerToken(c *gin.Context) string {
	parts := strings.SplitN(c.GetHeader("Authorization"), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// RequireAuth validates the access token and injects the user ID into the
// context. The token is read from the httpOnly access cookie; a Bearer header
// is still accepted for programmatic/API clients and tests. Refresh tokens are
// rejected here: they are only valid at POST /auth/refresh.
func RequireAuth(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenString := bearerToken(c)
		if tokenString == "" {
			// Ignore the cookie read error: an absent cookie simply means the
			// request is unauthenticated.
			tokenString, _ = c.Cookie(AccessCookieName)
		}
		if tokenString == "" {
			validation.RespondAuthError(c, "missing authentication")
			return
		}

		claims, err := ParseToken(tokenString, secret, TokenTypeAccess)
		if err != nil {
			validation.RespondAuthError(c, "invalid or expired token")
			return
		}

		c.Set(ctxUserIDKey, claims.UserID)
		c.Set(ctxUserRoleKey, claims.Role)
		c.Set(ctxClaimsKey, claims)
		c.Next()
	}
}

// GetClaims returns the parsed token claims populated by RequireAuth, or nil
// when the request is unauthenticated (or the middleware has not run).
func GetClaims(c *gin.Context) *Claims {
	if v, ok := c.Get(ctxClaimsKey); ok {
		if claims, ok := v.(*Claims); ok {
			return claims
		}
	}
	return nil
}

// RequireAdmin rejects the request unless the authenticated user has the
// 'admin' role. It must run after RequireAuth (or another middleware that
// populated the role from the token).
func RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if GetUserRole(c) != "admin" {
			validation.RespondError(c, "forbidden: admin access required", http.StatusForbidden)
			c.Abort()
			return
		}
		c.Next()
	}
}

// GetUserID returns the authenticated user's ID from the request context.
// Returns uuid.Nil when the middleware has not populated the context (e.g. unit tests).
func GetUserID(c *gin.Context) uuid.UUID {
	if v, ok := c.Get(ctxUserIDKey); ok {
		if id, ok := v.(uuid.UUID); ok {
			return id
		}
	}
	return uuid.Nil
}

// GetUserRole returns the authenticated user's role from the request context.
// Returns "" when the middleware has not populated the context.
func GetUserRole(c *gin.Context) string {
	if v, ok := c.Get(ctxUserRoleKey); ok {
		if role, ok := v.(string); ok {
			return role
		}
	}
	return ""
}
