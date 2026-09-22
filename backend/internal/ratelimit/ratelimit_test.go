package ratelimit

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

func TestAllowBurstThenDeny(t *testing.T) {
	l := New(Config{Rate: rate.Limit(0), Burst: 2, TTL: time.Minute})

	assert.True(t, l.Allow("a"))
	assert.True(t, l.Allow("a"))
	assert.False(t, l.Allow("a"))

	// Independent keys have independent buckets.
	assert.True(t, l.Allow("b"))
}

func TestNilLimiterAllowsEverything(t *testing.T) {
	var l *Limiter
	assert.True(t, l.Allow("anything"))
	l.Evict()
	// A nil limiter's janitor returns immediately without blocking.
	l.StartJanitor(make(chan struct{}), time.Second)
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	assert.Equal(t, 5, cfg.Burst)
	assert.Equal(t, 10*time.Minute, cfg.TTL)
	assert.Equal(t, rate.Every(6*time.Second), cfg.Rate)
	assert.Equal(t, defaultMaxKeys, cfg.MaxKeys)
}

// The per-identity failure budget must be far more permissive than the per-IP
// bucket: it is shared by every client, so a burst one IP can reach quickly
// would be an attacker-held lock on any chosen address.
func TestDefaultFailureConfig(t *testing.T) {
	cfg := DefaultFailureConfig()
	perIP := DefaultConfig()

	assert.GreaterOrEqual(t, cfg.Burst, 10*perIP.Burst)
	assert.Less(t, float64(cfg.Rate), float64(perIP.Rate), "the failure budget must refill no faster than the per-IP bucket")
	assert.Equal(t, perIP.TTL, cfg.TTL)
	assert.Equal(t, defaultMaxKeys, cfg.MaxKeys)
}

// TestLimiterBoundsTrackedKeys pins the memory bound: the cap is enforced by
// evicting the stalest bucket, NOT by refusing the new key. Failing closed for
// everyone once the map is full turned a flood of attacker-chosen keys into a
// global lockout of every client not seen recently.
func TestLimiterBoundsTrackedKeys(t *testing.T) {
	base := time.Now()
	l := New(Config{Rate: rate.Inf, Burst: 1, TTL: time.Minute, MaxKeys: 2})
	l.now = func() time.Time { return base }

	assert.True(t, l.Allow("a"))
	l.now = func() time.Time { return base.Add(time.Second) }
	assert.True(t, l.Allow("b"))
	require.Len(t, l.entries, 2)

	// The map is full and nothing is expired: the new key still gets its own
	// bucket, displacing the least recently used one ("a").
	l.now = func() time.Time { return base.Add(2 * time.Second) }
	assert.True(t, l.Allow("c"))
	assert.Len(t, l.entries, 2)
	assert.NotContains(t, l.entries, "a")
	assert.Contains(t, l.entries, "b")
	assert.Contains(t, l.entries, "c")

	// The evicted key starts fresh rather than inheriting a spent bucket, and
	// the key that was in use is untouched.
	assert.True(t, l.Allow("b"))
	assert.True(t, l.Allow("a"))

	// Expired entries are swept before the cap is enforced, freeing space.
	l.now = func() time.Time { return base.Add(2 * time.Minute) }
	assert.True(t, l.Allow("d"))
	assert.Len(t, l.entries, 1)
}

func TestResetReturnsBucketToFullBurst(t *testing.T) {
	l := New(Config{Rate: rate.Limit(0), Burst: 2, TTL: time.Minute})

	assert.True(t, l.Allow("a"))
	assert.True(t, l.Allow("a"))
	assert.False(t, l.Allow("a"))

	l.Reset("a")
	assert.True(t, l.Allow("a"))
	assert.True(t, l.Allow("a"))
	assert.False(t, l.Allow("a"))

	// Resetting an unknown key is a no-op, and a nil limiter tolerates both.
	l.Reset("never-seen")
	var nilLimiter *Limiter
	nilLimiter.Reset("anything")
}

func TestNewAppliesMinimums(t *testing.T) {
	l := New(Config{})
	assert.Equal(t, 1, l.cfg.Burst)
	assert.Equal(t, 10*time.Minute, l.cfg.TTL)
}

func TestStartJanitorEvictsUntilStopped(t *testing.T) {
	base := time.Now()
	l := New(Config{Rate: rate.Inf, Burst: 1, TTL: 20 * time.Millisecond})
	l.now = func() time.Time { return base }
	assert.True(t, l.Allow("stale"))
	l.now = func() time.Time { return base.Add(time.Hour) }

	stop := make(chan struct{})
	// A non-positive interval defaults to half the TTL.
	go l.StartJanitor(stop, 0)

	require.Eventually(t, func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		return len(l.entries) == 0
	}, time.Second, 5*time.Millisecond)

	close(stop)
}

func TestEvictRemovesIdleKeys(t *testing.T) {
	base := time.Now()
	l := New(Config{Rate: rate.Every(30 * time.Second), Burst: 1, TTL: time.Minute})
	l.now = func() time.Time { return base }

	assert.True(t, l.Allow("idle"))
	assert.True(t, l.Allow("active"))
	assert.Len(t, l.entries, 2)

	// "idle" is not touched; "active" is used again at the later time.
	l.now = func() time.Time { return base.Add(2 * time.Minute) }
	assert.True(t, l.Allow("active"))
	l.Evict()

	assert.NotContains(t, l.entries, "idle")
	assert.Contains(t, l.entries, "active")
}
