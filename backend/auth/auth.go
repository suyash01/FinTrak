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
	// tokenTTL is how long an issued JWT stays valid. Kept short because the
	// role is embedded in the token and cannot be revoked before it expires.
	tokenTTL = 2 * time.Hour
	// tokenIssuer and tokenAudience are validated on every request so tokens
	// minted for another service (or with the same secret but different intent)
	// are rejected.
	tokenIssuer   = "fintrak"
	tokenAudience = "fintrak-api"
)

// Claims is the JWT payload for FinTrak tokens: the user ID, role, and standard
// registered claims.
type Claims struct {
	UserID uuid.UUID `json:"user_id"`
	Role   string    `json:"role"`
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

// GenerateToken signs an HS256 JWT for the given user and role using the secret.
func GenerateToken(userID uuid.UUID, role, secret string) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID: userID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tokenIssuer,
			Subject:   userID.String(),
			Audience:  jwt.ClaimStrings{tokenAudience},
			ID:        uuid.NewString(),
			ExpiresAt: jwt.NewNumericDate(now.Add(tokenTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// AuthCookieName is the httpOnly session cookie that carries the JWT.
const AuthCookieName = "fintrak_token"

// SetAuthCookie writes the session JWT as an httpOnly, SameSite=Lax cookie so
// it is unreadable from JavaScript. Pass secure=true in production (HTTPS).
// SameSite=Lax is the CSRF defense: browsers do not attach the cookie to
// cross-site POST/PUT/PATCH/DELETE requests.
func SetAuthCookie(c *gin.Context, token string, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     AuthCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(tokenTTL.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearAuthCookie expires the session cookie.
func ClearAuthCookie(c *gin.Context, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     AuthCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
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

// RequireAuth validates the session token and injects the user ID into the
// context. The token is read from the httpOnly session cookie; a Bearer header
// is still accepted for programmatic/API clients and tests.
func RequireAuth(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenString := bearerToken(c)
		if tokenString == "" {
			// Ignore the cookie read error: an absent cookie simply means the
			// request is unauthenticated.
			tokenString, _ = c.Cookie(AuthCookieName)
		}
		if tokenString == "" {
			validation.RespondAuthError(c, "missing authentication")
			return
		}

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
			validation.RespondAuthError(c, "invalid or expired token")
			return
		}

		claims, ok := token.Claims.(*Claims)
		if !ok {
			validation.RespondAuthError(c, "invalid token claims")
			return
		}

		c.Set(ctxUserIDKey, claims.UserID)
		c.Set(ctxUserRoleKey, claims.Role)
		c.Next()
	}
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
