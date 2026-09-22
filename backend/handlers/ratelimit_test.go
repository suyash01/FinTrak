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

// withAuthLimiter installs l as the package-level per-IP limiter for the
// duration of the test and restores whatever was there before.
func withAuthLimiter(t *testing.T, l *ratelimit.Limiter) {
	t.Helper()
	original := authRateLimiter
	authRateLimiter = l
	t.Cleanup(func() { authRateLimiter = original })
}

// withAuthFailureLimiter installs l as the package-level per-identity failure
// budget for the duration of the test, through the same setter main uses.
func withAuthFailureLimiter(t *testing.T, l *ratelimit.Limiter) {
	t.Helper()
	original := authFailureLimiter
	SetAuthFailureLimiter(l)
	t.Cleanup(func() { SetAuthFailureLimiter(original) })
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
	assert.True(t, allowAuthAttempt(c, "login"))
	assert.Empty(t, w.Body.String())
}

func TestAllowAuthAttemptBlocksAfterBurst(t *testing.T) {
	withAuthLimiter(t, ratelimit.New(ratelimit.Config{Rate: rate.Limit(0), Burst: 1, TTL: time.Minute}))

	c1, _ := authContext("10.0.0.1")
	assert.True(t, allowAuthAttempt(c1, "login"))

	c2, w2 := authContext("10.0.0.1")
	assert.False(t, allowAuthAttempt(c2, "login"))
	assert.Equal(t, http.StatusTooManyRequests, w2.Code)
	assert.Equal(t, "30", w2.Header().Get("Retry-After"))
	assert.Contains(t, w2.Body.String(), "too many attempts")
}

// TestAllowAuthAttemptIsKeyedOnIPOnly pins the fix for the attacker-held
// per-account lock: one IP exhausting its own bucket must not affect another
// IP's attempt at the same address, because the account dimension no longer
// exists on this path. (The per-identity budget lives in recordAuthFailure and
// is charged only for failures.)
func TestAllowAuthAttemptIsKeyedOnIPOnly(t *testing.T) {
	withAuthLimiter(t, ratelimit.New(ratelimit.Config{Rate: rate.Limit(0), Burst: 1, TTL: time.Minute}))

	// The attacker drains its own IP bucket for the victim's address.
	c1, _ := authContext("10.0.0.1")
	assert.True(t, allowAuthAttempt(c1, "login"))
	c2, w2 := authContext("10.0.0.1")
	assert.False(t, allowAuthAttempt(c2, "login"))
	assert.Equal(t, http.StatusTooManyRequests, w2.Code)

	// The victim from a different IP is unaffected.
	victim, _ := authContext("10.0.0.2")
	assert.True(t, allowAuthAttempt(victim, "login"))
}

func TestAllowAuthAttemptSeparatesActions(t *testing.T) {
	withAuthLimiter(t, ratelimit.New(ratelimit.Config{Rate: rate.Limit(0), Burst: 1, TTL: time.Minute}))

	c1, _ := authContext("10.0.0.1")
	assert.True(t, allowAuthAttempt(c1, "login"))

	c2, _ := authContext("10.0.0.1")
	assert.True(t, allowAuthAttempt(c2, "register"))
}

func TestRecordAuthFailureNilLimiterAllows(t *testing.T) {
	withAuthFailureLimiter(t, nil)

	c, w := authContext("10.0.0.1")
	assert.True(t, recordAuthFailure(c, "login", "alice@example.com"))
	assert.Empty(t, w.Body.String())
	// The refund path is a no-op rather than a panic.
	refundAuthFailures("login", "alice@example.com")
}

func TestRecordAuthFailureExhaustsBudget(t *testing.T) {
	withAuthFailureLimiter(t, ratelimit.New(ratelimit.Config{Rate: rate.Limit(0), Burst: 2, TTL: time.Minute}))

	c1, _ := authContext("10.0.0.1")
	assert.True(t, recordAuthFailure(c1, "login", "alice@example.com"))

	c2, _ := authContext("10.0.0.2") // a second IP does not reset the budget
	assert.True(t, recordAuthFailure(c2, "login", "alice@example.com"))

	c3, w3 := authContext("10.0.0.3")
	assert.False(t, recordAuthFailure(c3, "login", "alice@example.com"))
	assert.Equal(t, http.StatusTooManyRequests, w3.Code)
	assert.Equal(t, "30", w3.Header().Get("Retry-After"))
	assert.Contains(t, w3.Body.String(), "too many attempts")

	// A different identity keeps its own budget.
	c4, _ := authContext("10.0.0.3")
	assert.True(t, recordAuthFailure(c4, "login", "bob@example.com"))
}

// TestRecordAuthFailureNormalizesIdentity keeps differently-cased spellings of
// one address on one budget, matching the lowercasing registration applies.
func TestRecordAuthFailureNormalizesIdentity(t *testing.T) {
	withAuthFailureLimiter(t, ratelimit.New(ratelimit.Config{Rate: rate.Limit(0), Burst: 1, TTL: time.Minute}))

	c1, _ := authContext("10.0.0.1")
	assert.True(t, recordAuthFailure(c1, "login", "  Alice@Example.COM "))

	c2, w2 := authContext("10.0.0.1")
	assert.False(t, recordAuthFailure(c2, "login", "alice@example.com"))
	assert.Equal(t, http.StatusTooManyRequests, w2.Code)
}

// TestRefundAuthFailuresClearsBudget is the availability half of finding 1: a
// successful login refunds the identity's failures, so an attacker cannot hold
// a third party's budget at zero by keeping the guesses coming.
func TestRefundAuthFailuresClearsBudget(t *testing.T) {
	withAuthFailureLimiter(t, ratelimit.New(ratelimit.Config{Rate: rate.Limit(0), Burst: 1, TTL: time.Minute}))

	c1, _ := authContext("10.0.0.1")
	assert.True(t, recordAuthFailure(c1, "login", "victim@example.com"))

	c2, w2 := authContext("10.0.0.1")
	assert.False(t, recordAuthFailure(c2, "login", "VICTIM@example.com"))
	assert.Equal(t, http.StatusTooManyRequests, w2.Code)

	refundAuthFailures("login", "victim@example.com")

	c3, _ := authContext("10.0.0.1")
	assert.True(t, recordAuthFailure(c3, "login", "victim@example.com"))
}
