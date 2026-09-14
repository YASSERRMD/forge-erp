package platform

import (
	"fmt"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/go-chi/chi/v5"
)

// Metrics is a minimal Prometheus-compatible request counter set (OTel SDK wiring
// lands with the production observability stack; this endpoint keeps per-route
// visibility from day one without extra dependencies).
type Metrics struct {
	mu     sync.Mutex
	counts map[string]*atomic.Uint64
}

// NewMetrics builds an empty set.
func NewMetrics() *Metrics { return &Metrics{counts: map[string]*atomic.Uint64{}} }

func (m *Metrics) inc(route string) {
	m.mu.Lock()
	c, ok := m.counts[route]
	if !ok {
		c = &atomic.Uint64{}
		m.counts[route] = c
	}
	m.mu.Unlock()
	c.Add(1)
}

// Instrument counts requests by method + chi route pattern. The pattern is
// read after the handler runs (chi matches the route before executing the
// middleware chain, so it is populated by then); unmatched requests share
// one bucket instead of exploding cardinality on raw paths.
func (m *Metrics) Instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		pattern := chi.RouteContext(r.Context()).RoutePattern()
		if pattern == "" {
			pattern = "unmatched"
		}
		m.inc(r.Method + " " + pattern)
	})
}

// Handler exposes build info + counters in Prometheus text format.
func (m *Metrics) Handler(build BuildInfo) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		m.mu.Lock()
		keys := make([]string, 0, len(m.counts))
		for k := range m.counts {
			keys = append(keys, k)
		}
		m.mu.Unlock()
		sort.Strings(keys)
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintf(w, "# HELP forgeerp_build_info Build metadata\n")
		fmt.Fprintf(w, "# TYPE forgeerp_build_info gauge\n")
		fmt.Fprintf(w, "forgeerp_build_info{version=%q,commit=%q} 1\n", build.Version, build.Commit)
		fmt.Fprintf(w, "# HELP forgeerp_http_requests_total Requests by route\n")
		fmt.Fprintf(w, "# TYPE forgeerp_http_requests_total counter\n")
		for _, k := range keys {
			m.mu.Lock()
			v := m.counts[k].Load()
			m.mu.Unlock()
			fmt.Fprintf(w, "forgeerp_http_requests_total{route=%q} %d\n", k, v)
		}
	}
}
