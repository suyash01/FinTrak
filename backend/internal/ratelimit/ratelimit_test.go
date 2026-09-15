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

func TestLimiterBoundsTrackedKeys(t *testing.T) {
	base := time.Now()
	l := New(Config{Rate: rate.Inf, Burst: 1, TTL: time.Minute, MaxKeys: 2})
	l.now = func() time.Time { return base }

	assert.True(t, l.Allow("a"))
	assert.True(t, l.Allow("b"))
	// The map is full and nothing is expired, so a new key is refused rather
	// than growing memory without bound.
	assert.False(t, l.Allow("c"))
	// Existing keys are unaffected.
	assert.True(t, l.Allow("a"))
	assert.Len(t, l.entries, 2)

	// Expired entries are swept before the cap is enforced, freeing space.
	l.now = func() time.Time { return base.Add(2 * time.Minute) }
	assert.True(t, l.Allow("c"))
	assert.Len(t, l.entries, 1)
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
