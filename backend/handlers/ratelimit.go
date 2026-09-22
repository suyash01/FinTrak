package handlers

import (
	"net/http"
	"strings"

	"github.com/fintrak/backend/internal/ratelimit"
	"github.com/fintrak/backend/internal/validation"
	"github.com/gin-gonic/gin"
)

// authRateLimiter, when installed by main, throttles unauthenticated auth
// endpoints by client IP. It is nil in unit tests, which disables rate limiting
// so existing handler tests are unaffected.
var authRateLimiter *ratelimit.Limiter

// authFailureLimiter, when installed by main, carries the per-identity failure
// budget for failed credential checks. It is a separate limiter because it has
// its own policy (see ratelimit.DefaultFailureConfig) and because its key is
// attacker-chosen: sharing the per-IP limiter's map would let a flood of
// account keys crowd out the IP buckets.
var authFailureLimiter *ratelimit.Limiter

// SetAuthRateLimiter installs the limiter used by Register and Login for the
// per-IP dimension. Passing nil disables rate limiting.
func SetAuthRateLimiter(l *ratelimit.Limiter) { authRateLimiter = l }

// SetAuthFailureLimiter installs the per-identity failure budget. Passing nil
// disables it.
func SetAuthFailureLimiter(l *ratelimit.Limiter) { authFailureLimiter = l }

// allowAuthAttempt consumes one token from the caller's per-IP bucket. It writes
// a generic 429 and returns false when the bucket is exhausted.
//
// The key is the client IP and nothing else: a per-account bucket on this path
// was attacker-held state — one IP could drain `login:acct:<victim>` and keep it
// drained (consuming one token per refill interval, which is exactly the
// throughput its own IP bucket allows) so that the victim's attempts from a
// pristine IP were refused indefinitely. Credential throttling for a single
// identity is the failure budget below, which cannot refuse a correct password.
func allowAuthAttempt(c *gin.Context, action string) bool {
	if authRateLimiter == nil {
		return true
	}
	if authRateLimiter.Allow(action + ":ip:" + c.ClientIP()) {
		return true
	}
	respondTooManyAttempts(c)
	return false
}

// recordAuthFailure charges the per-identity failure budget after a rejected
// credential check and reports whether the attempt may still be answered with a
// credential verdict. When the budget is exhausted it writes the same generic
// 429 as the per-IP bucket and returns false.
//
// Callers MUST verify the credentials first and return early on success. The
// ordering is the whole point: a correct password is therefore never refused by
// a drained per-identity budget, so nobody can lock an account out — not with a
// handful of requests and not with a flood.
//
// Residual trade-off, stated plainly: the budget is shared by every client, so a
// distributed (or patient) attacker can still exhaust it and make an identity's
// *wrong* passwords answer 429 instead of 401 — but only after 50 failures
// within the refill window, and never for a correct password, which also refunds
// the budget (see refundAuthFailures). What is lost relative to a hard lock is
// that the budget cannot stop the bcrypt work of the guess that exhausts it,
// because the guess has to be verified before we know it was wrong.
func recordAuthFailure(c *gin.Context, action, email string) bool {
	if authFailureLimiter == nil {
		return true
	}
	if authFailureLimiter.Allow(authFailureKey(action, email)) {
		return true
	}
	respondTooManyAttempts(c)
	return false
}

// refundAuthFailures clears an identity's failure budget after a successful
// login. The identity has proven ownership, and the failures charged against it
// came from somebody else's guesses; without the refund an attacker could keep
// the budget pinned at zero for as long as it kept guessing.
func refundAuthFailures(action, email string) {
	if authFailureLimiter == nil {
		return
	}
	authFailureLimiter.Reset(authFailureKey(action, email))
}

// authFailureKey normalizes the identity so differently-cased spellings of one
// address share a single budget (registration stores emails lowercased).
func authFailureKey(action, email string) string {
	return action + ":fail:" + strings.ToLower(strings.TrimSpace(email))
}

// respondTooManyAttempts gives a generic response so the throttle cannot be used
// to enumerate accounts or to probe which dimension tripped.
func respondTooManyAttempts(c *gin.Context) {
	c.Header("Retry-After", "30")
	validation.RespondError(c, "too many attempts; try again later", http.StatusTooManyRequests)
}
