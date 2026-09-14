package platform

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiter(t *testing.T) {
	now := time.Now().UTC()
	l := NewRateLimiter(1, 3)
	l.Now = func() time.Time { return now }
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := l.Limit(ok)
	hit := func() int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	for i := 0; i < 3; i++ {
		if code := hit(); code != http.StatusOK {
			t.Fatalf("burst %d: code=%d", i, code)
		}
	}
	if code := hit(); code != http.StatusTooManyRequests {
		t.Fatalf("over burst: code=%d want 429", code)
	}
	// Refill after 2s → allowed again.
	now = now.Add(2 * time.Second)
	if code := hit(); code != http.StatusOK {
		t.Fatalf("refilled: code=%d", code)
	}
	// Separate IP unaffected.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req.RemoteAddr = "10.0.0.2:1234"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("other ip: code=%d", rec.Code)
	}
}

// TestRateLimiterEviction proves idle buckets are reaped (Phase 0 task 7):
// the per-IP map must not grow with every address ever seen.
func TestRateLimiterEviction(t *testing.T) {
	now := time.Now().UTC()
	l := NewRateLimiter(1, 3)
	l.Now = func() time.Time { return now }
	if !l.Allow("10.0.0.1") || !l.Allow("10.0.0.2") {
		t.Fatal("initial allow failed")
	}
	if len(l.buckets) != 2 {
		t.Fatalf("buckets = %d want 2", len(l.buckets))
	}
	// Fresh activity is not idle.
	now = now.Add(time.Minute)
	if n := l.evictIdle(now, 10*time.Minute); n != 0 {
		t.Fatalf("evicted %d fresh buckets", n)
	}
	// After the TTL with no traffic, both go.
	now = now.Add(11 * time.Minute)
	if n := l.evictIdle(now, 10*time.Minute); n != 2 {
		t.Fatalf("evicted = %d want 2", n)
	}
	if len(l.buckets) != 0 {
		t.Fatalf("buckets = %d want 0", len(l.buckets))
	}
	// Ticker lifecycle: start/stop without deadlock.
	stop := l.StartCleanup(time.Millisecond, time.Minute)
	stop()
}
