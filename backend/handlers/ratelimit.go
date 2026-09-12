package handlers

import (
	"net/http"
	"strings"

	"github.com/fintrak/backend/internal/ratelimit"
	"github.com/fintrak/backend/internal/validation"
	"github.com/gin-gonic/gin"
)

// authRateLimiter, when installed by main, throttles unauthenticated auth
// endpoints. It is nil in unit tests, which disables rate limiting so existing
// handler tests are unaffected.
var authRateLimiter *ratelimit.Limiter

// SetAuthRateLimiter installs the limiter used by Register and Login. Passing
// nil disables rate limiting.
func SetAuthRateLimiter(l *ratelimit.Limiter) { authRateLimiter = l }

// allowAuthAttempt checks both a per-IP and a per-identity bucket, so one IP
// cannot spray many accounts and many IPs cannot target one account. It writes
// a generic 429 and returns false when either bucket is exhausted.
func allowAuthAttempt(c *gin.Context, action, email string) bool {
	if authRateLimiter == nil {
		return true
	}
	identity := strings.ToLower(strings.TrimSpace(email))
	if authRateLimiter.Allow(action+":ip:"+c.ClientIP()) &&
		authRateLimiter.Allow(action+":acct:"+identity) {
		return true
	}
	// The limiter gives a generic response so it cannot be used to enumerate
	// accounts or probe which dimension tripped.
	c.Header("Retry-After", "30")
	validation.RespondError(c, "too many attempts; try again later", http.StatusTooManyRequests)
	return false
}
