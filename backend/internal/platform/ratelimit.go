package platform

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// RateLimiter is a per-IP token-bucket limiter (Phase 10 hardening: brute-force
// protection on auth endpoints, abuse caps elsewhere). Zero dependencies.
type RateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	Rate     float64 // tokens per second
	Capacity float64 // burst size
	Now      func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewRateLimiter builds a limiter (e.g. 5/sec burst 20 for login).
func NewRateLimiter(rate, capacity float64) *RateLimiter {
	return &RateLimiter{buckets: map[string]*bucket{}, Rate: rate, Capacity: capacity,
		Now: time.Now}
}

// Allow consumes one token for ip; false means reject with 429.
func (l *RateLimiter) Allow(ip string) bool {
	now := l.Now().UTC()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[ip]
	if !ok {
		b = &bucket{tokens: l.Capacity, last: now}
		l.buckets[ip] = b
	}
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * l.Rate
		if b.tokens > l.Capacity {
			b.tokens = l.Capacity
		}
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Limit wraps a handler with 429 rejection + Retry-After hint.
func (l *RateLimiter) Limit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}
		if !l.Allow(ip) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
