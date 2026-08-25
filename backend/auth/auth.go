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
	// tokenTTL is how long an issued JWT stays valid.
	tokenTTL = 24 * time.Hour
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
	claims := Claims{
		UserID:    userID,
		Role:      role,
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(tokenTTL)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// RequireAuth validates the Bearer token and injects the user ID into the context.
func RequireAuth(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" {
			validation.RespondAuthError(c, "missing authorization header")
			return
		}

		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			validation.RespondAuthError(c, "invalid authorization header")
			return
		}

		token, err := jwt.ParseWithClaims(parts[1], &Claims{}, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, errors.New("unexpected signing method")
			}
			return []byte(secret), nil
		})
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
