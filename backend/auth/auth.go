// Package auth provides password hashing, JWT issuance/validation, and the Gin
// middleware that protects authenticated routes. Authenticated user IDs and
// roles are stashed in the request context for handlers to consume.
package auth

import (
	"errors"
	"log/slog"
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
	// tokenTTL is how long an issued JWT stays valid. Kept short because the
	// role is embedded in the token and cannot be revoked before it expires,
	// and because sliding renewal (see RenewSession) refreshes it on activity.
	tokenTTL = 2 * time.Hour
	// sessionTTL is the absolute maximum lifetime of a session regardless of
	// activity. Sliding renewal extends a session as the user works, but never
	// past this deadline; once it passes the user must log in again. The
	// deadline travels inside the token (Claims.SessionExpiresAt) so no
	// server-side session store is required.
	sessionTTL = 30 * 24 * time.Hour
	// renewThreshold is how close an access token must be to expiry before
	// RenewSession mints a replacement. Renewing on every request would churn
	// tokens for no benefit.
	renewThreshold = 30 * time.Minute
	// tokenIssuer and tokenAudience are validated on every request so tokens
	// minted for another service (or with the same secret but different intent)
	// are rejected.
	tokenIssuer   = "fintrak"
	tokenAudience = "fintrak-api"
)

// Claims is the JWT payload for FinTrak tokens: the user ID, role, the absolute
// session deadline, and standard registered claims.
type Claims struct {
	UserID uuid.UUID `json:"user_id"`
	Role   string    `json:"role"`
	// SessionExpiresAt is the absolute deadline of the whole session. Sliding
	// renewal carries it forward unchanged, so activity can extend a session up
	// to — but never beyond — this instant. Tokens issued before sliding renewal
	// existed may omit it; such tokens are accepted but never renewed.
	SessionExpiresAt *jwt.NumericDate `json:"session_exp,omitempty"`
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
// It starts a brand-new session with a fresh absolute deadline (sessionTTL).
func GenerateToken(userID uuid.UUID, role, secret string) (string, error) {
	return generateToken(userID, role, secret, time.Now().Add(sessionTTL))
}

// generateToken signs a token whose session deadline is sessionExpiresAt. The
// access token's own expiry is capped at that deadline so a token can never
// outlive its session even if it is issued during the final renewal window.
func generateToken(userID uuid.UUID, role, secret string, sessionExpiresAt time.Time) (string, error) {
	now := time.Now()
	expiresAt := now.Add(tokenTTL)
	if sessionExpiresAt.Before(expiresAt) {
		expiresAt = sessionExpiresAt
	}

	claims := Claims{
		UserID:           userID,
		Role:             role,
		SessionExpiresAt: jwt.NewNumericDate(sessionExpiresAt),
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

		// Enforce the absolute session deadline. A token is normally short-lived
		// and capped at this instant at issuance, but checking it here keeps the
		// bound authoritative even if a token was minted with a longer expiry.
		if claims.SessionExpiresAt != nil && !time.Now().Before(claims.SessionExpiresAt.Time) {
			validation.RespondAuthError(c, "session expired")
			return
		}

		c.Set(ctxUserIDKey, claims.UserID)
		c.Set(ctxUserRoleKey, claims.Role)
		c.Set(ctxClaimsKey, claims)
		c.Next()
	}
}

// RenewSession is the sliding-session middleware. When an authenticated request
// arrives with an access token that is close to expiry, it transparently mints a
// replacement and sets it on the response, so an active user is never logged out
// mid-use. The session's absolute deadline (Claims.SessionExpiresAt) is carried
// forward unchanged, so renewal can extend a session but never past the cap.
//
// It must run after RequireAuth, which populates the parsed claims in the
// request context. Tokens that predate sliding renewal (no session deadline) are
// left alone and simply expire as before.
func RenewSession(secret string, secure bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := GetClaims(c)
		if claims == nil || claims.ExpiresAt == nil || claims.SessionExpiresAt == nil {
			c.Next()
			return
		}

		now := time.Now()
		// Once the absolute deadline is within the renewal window, stop renewing:
		// the token is already capped there and reissuing would repeat on every
		// request. The user will be asked to log in when it elapses.
		if claims.SessionExpiresAt.Time.Sub(now) <= renewThreshold {
			c.Next()
			return
		}
		// Still comfortably valid — nothing to do.
		if now.Add(renewThreshold).Before(claims.ExpiresAt.Time) {
			c.Next()
			return
		}

		token, err := generateToken(claims.UserID, claims.Role, secret, claims.SessionExpiresAt.Time)
		if err != nil {
			slog.Error("renewing session", slog.String("error", err.Error()))
			c.Next()
			return
		}
		SetAuthCookie(c, token, secure)
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
