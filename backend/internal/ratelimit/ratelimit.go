// Package ratelimit provides a small, concurrency-safe, keyed token-bucket
// limiter used to slow brute-force attempts against unauthenticated endpoints
// such as login and registration.
package ratelimit

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// defaultMaxKeys caps how many distinct keys a limiter tracks, bounding memory
// even if keys are attacker-controlled.
const defaultMaxKeys = 10000

// Config controls a Limiter's refill rate, burst size, idle-key retention, and
// maximum tracked keys.
type Config struct {
	Rate    rate.Limit
	Burst   int
	TTL     time.Duration
	MaxKeys int
}

// DefaultConfig returns the policy for authentication endpoints: a burst of 5
// requests refilling at one request every 6 seconds (10/min), with idle keys
// evicted after 10 minutes and at most 10,000 tracked keys.
func DefaultConfig() Config {
	return Config{
		Rate:    rate.Every(6 * time.Second),
		Burst:   5,
		TTL:     10 * time.Minute,
		MaxKeys: defaultMaxKeys,
	}
}

// DefaultFailureConfig returns the policy for a per-identity *failure* budget,
// the second dimension of the auth throttle. It is deliberately far more
// permissive than DefaultConfig: the per-IP bucket is what bounds an attacker's
// throughput, while this budget only decides how many rejected credential
// checks an identity keeps getting an answer for. The high burst matters
// because the budget is shared by every client — sizing it like the per-IP
// bucket is what let a single IP lock a chosen address out.
func DefaultFailureConfig() Config {
	return Config{
		Rate:    rate.Every(30 * time.Second),
		Burst:   50,
		TTL:     10 * time.Minute,
		MaxKeys: defaultMaxKeys,
	}
}

type entry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// Limiter is a keyed token-bucket limiter. Call New to construct one.
type Limiter struct {
	mu      sync.Mutex
	entries map[string]*entry
	cfg     Config
	now     func() time.Time
}

// New returns a Limiter for the given policy, applying safe minimums.
func New(cfg Config) *Limiter {
	if cfg.Burst < 1 {
		cfg.Burst = 1
	}
	if cfg.TTL <= 0 {
		cfg.TTL = 10 * time.Minute
	}
	if cfg.MaxKeys < 1 {
		cfg.MaxKeys = defaultMaxKeys
	}
	return &Limiter{
		entries: make(map[string]*entry),
		cfg:     cfg,
		now:     time.Now,
	}
}

// Allow reports whether a request identified by key may proceed, consuming one
// token from that key's bucket. A nil Limiter allows everything.
//
// The tracked-key cap bounds memory without ever refusing a request because of
// it: when the map is full the stalest bucket (by last use) is dropped so the
// new key takes its place. Failing closed instead — refusing every key that is
// not already tracked — turned a flood of attacker-chosen keys into a global
// lockout of every client that had not been seen recently.
func (l *Limiter) Allow(key string) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	e, ok := l.entries[key]
	if !ok {
		if len(l.entries) >= l.cfg.MaxKeys {
			l.evictLocked(now)
			if len(l.entries) >= l.cfg.MaxKeys {
				l.evictStalestLocked()
			}
		}
		e = &entry{limiter: rate.NewLimiter(l.cfg.Rate, l.cfg.Burst)}
		l.entries[key] = e
	}
	e.lastSeen = now
	return e.limiter.AllowN(now, 1)
}

// Reset drops a key's bucket, returning it to its full burst. It is the refund
// path for a failure budget: a caller that proves ownership (a correct login
// password) clears the failures accumulated against its identity, so a third
// party can never hold that identity's budget at zero.
func (l *Limiter) Reset(key string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}

// Evict removes buckets idle for longer than the configured TTL. It is safe to
// call concurrently and is intended to be invoked periodically.
func (l *Limiter) Evict() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.evictLocked(l.now())
}

// evictLocked drops buckets whose last use predates the TTL cutoff. The caller
// must hold l.mu.
func (l *Limiter) evictLocked(now time.Time) {
	cutoff := now.Add(-l.cfg.TTL)
	for key, e := range l.entries {
		if e.lastSeen.Before(cutoff) {
			delete(l.entries, key)
		}
	}
}

// evictStalestLocked drops the least recently used bucket, making room for a
// new key when the map is at capacity and nothing has expired. The caller must
// hold l.mu.
func (l *Limiter) evictStalestLocked() {
	var (
		stalestKey  string
		stalestSeen time.Time
	)
	for key, e := range l.entries {
		if stalestKey == "" || e.lastSeen.Before(stalestSeen) {
			stalestKey, stalestSeen = key, e.lastSeen
		}
	}
	if stalestKey != "" {
		delete(l.entries, stalestKey)
	}
}

// StartJanitor calls Evict every interval until stop is closed. A non-positive
// interval defaults to half the TTL.
func (l *Limiter) StartJanitor(stop <-chan struct{}, interval time.Duration) {
	if l == nil {
		return
	}
	if interval <= 0 {
		interval = l.cfg.TTL / 2
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			l.Evict()
		}
	}
}
