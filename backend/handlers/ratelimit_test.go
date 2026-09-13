package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fintrak/backend/internal/ratelimit"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"golang.org/x/time/rate"
)

// withAuthLimiter installs l as the package-level limiter for the duration of
// the test and restores whatever was there before.
func withAuthLimiter(t *testing.T, l *ratelimit.Limiter) {
	t.Helper()
	original := authRateLimiter
	authRateLimiter = l
	t.Cleanup(func() { authRateLimiter = original })
}

func authContext(ip string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	c.Request.RemoteAddr = ip + ":12345"
	return c, w
}

func TestAllowAuthAttemptNilLimiterAllows(t *testing.T) {
	withAuthLimiter(t, nil)

	c, w := authContext("10.0.0.1")
	assert.True(t, allowAuthAttempt(c, "login", "alice@example.com"))
	assert.Empty(t, w.Body.String())
}

func TestAllowAuthAttemptBlocksAfterBurst(t *testing.T) {
	withAuthLimiter(t, ratelimit.New(ratelimit.Config{Rate: rate.Limit(0), Burst: 1, TTL: time.Minute}))

	c1, _ := authContext("10.0.0.1")
	assert.True(t, allowAuthAttempt(c1, "login", "alice@example.com"))

	c2, w2 := authContext("10.0.0.1")
	assert.False(t, allowAuthAttempt(c2, "login", "alice@example.com"))
	assert.Equal(t, http.StatusTooManyRequests, w2.Code)
	assert.Equal(t, "30", w2.Header().Get("Retry-After"))
	assert.Contains(t, w2.Body.String(), "too many attempts")
}

// TestAllowAuthAttemptLimitsPerAccountAcrossIPs proves the per-account bucket
// stops a distributed spray: a fresh IP has its own per-IP bucket but shares
// the target account's bucket.
func TestAllowAuthAttemptLimitsPerAccountAcrossIPs(t *testing.T) {
	withAuthLimiter(t, ratelimit.New(ratelimit.Config{Rate: rate.Limit(0), Burst: 1, TTL: time.Minute}))

	c1, _ := authContext("10.0.0.1")
	assert.True(t, allowAuthAttempt(c1, "login", "victim@example.com"))

	c2, w2 := authContext("10.0.0.2")
	assert.False(t, allowAuthAttempt(c2, "login", "victim@example.com"))
	assert.Equal(t, http.StatusTooManyRequests, w2.Code)
}

func TestAllowAuthAttemptNormalizesAccountKey(t *testing.T) {
	withAuthLimiter(t, ratelimit.New(ratelimit.Config{Rate: rate.Limit(0), Burst: 1, TTL: time.Minute}))

	c1, _ := authContext("10.0.0.1")
	assert.True(t, allowAuthAttempt(c1, "login", "  Alice@Example.COM "))

	// Different IP, differently-cased address -> same account bucket.
	c2, w2 := authContext("10.0.0.2")
	assert.False(t, allowAuthAttempt(c2, "login", "alice@example.com"))
	assert.Equal(t, http.StatusTooManyRequests, w2.Code)
}

func TestAllowAuthAttemptSeparatesActions(t *testing.T) {
	withAuthLimiter(t, ratelimit.New(ratelimit.Config{Rate: rate.Limit(0), Burst: 1, TTL: time.Minute}))

	c1, _ := authContext("10.0.0.1")
	assert.True(t, allowAuthAttempt(c1, "login", "alice@example.com"))

	c2, _ := authContext("10.0.0.1")
	assert.True(t, allowAuthAttempt(c2, "register", "alice@example.com"))
}
