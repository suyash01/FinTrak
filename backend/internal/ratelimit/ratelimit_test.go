package ratelimit

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
