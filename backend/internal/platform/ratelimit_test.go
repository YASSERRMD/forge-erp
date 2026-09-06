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
