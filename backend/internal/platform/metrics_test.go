package platform

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsEndpoint(t *testing.T) {
	m := NewMetrics()
	build := BuildInfo{Version: "test", Commit: "abc"}
	h := m.Instrument(Router(build))
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
