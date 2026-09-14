package platform

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestMetricsEndpoint(t *testing.T) {
	m := NewMetrics()
	build := BuildInfo{Version: "test", Commit: "abc"}
	h := Router(build, nil, m.Instrument)
	// Hit a route twice, then scrape.
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
	}
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	m.Handler(build).ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, `forgeerp_build_info{version="test",commit="abc"} 1`) {
		t.Fatalf("build info missing:\n%s", body)
	}
	if !strings.Contains(body, `forgeerp_http_requests_total{route="GET /healthz"} 2`) {
		t.Fatalf("counter missing:\n%s", body)
	}
}

// TestMetricsKeyedOnPattern proves per-route (not per-path) counting
// (Phase 0 task 6): /items/1 and /items/2 collapse into one GET /items/{id}
// bucket, and unmatched paths share a single bucket instead of exploding
// cardinality.
func TestMetricsKeyedOnPattern(t *testing.T) {
	m := NewMetrics()
	r := chi.NewRouter()
	r.Use(m.Instrument)
	r.Get("/items/{id}", func(w http.ResponseWriter, r *http.Request) {})
	h := r
	for _, p := range []string{"/items/1", "/items/2"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", p, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	build := BuildInfo{}
	rec = httptest.NewRecorder()
	m.Handler(build).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `forgeerp_http_requests_total{route="GET /items/{id}"} 2`) {
		t.Fatalf("pattern bucket missing:\n%s", body)
	}
	if strings.Contains(body, "/items/1") || strings.Contains(body, "/items/2") {
		t.Fatalf("raw-path buckets leaked:\n%s", body)
	}
	if !strings.Contains(body, `forgeerp_http_requests_total{route="GET unmatched"} 1`) {
		t.Fatalf("unmatched bucket missing:\n%s", body)
	}
}
