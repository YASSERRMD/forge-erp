package platform

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Router builds the base HTTP router with platform middleware and health endpoints.
// Extra middlewares must be supplied here (chi panics if Use follows routes).
// readyPing gates /readyz on the database (nil leaves the pre-existing
// always-ok behavior for contexts without a pool, e.g. unit tests).
func Router(build BuildInfo, readyPing func() error, middlewares ...func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	// Trusted-proxy client IP (Phase 0 task 7): X-Forwarded-For is honored
	// only from FERP_TRUSTED_PROXIES (default loopback + RFC 1918).
	r.Use(RealIPFromTrusted(TrustedProxiesFromEnv()))
	r.Use(middleware.Recoverer)
	r.Use(SecurityHeaders)
	r.Use(middlewares...)

	r.Get("/healthz", HealthHandler(build, false))
	var pings []func() error
	if readyPing != nil {
		pings = append(pings, readyPing)
	}
	r.Get("/readyz", HealthHandler(build, true, pings...))
	return r
}

// BuildInfo is stamped at build time via -ldflags (or defaults for dev).
type BuildInfo struct {
	Version string
	Commit  string
}

// HealthHandler serves /healthz (liveness) and /readyz (readiness probe hook:
// pass checkDB to gate on a DB ping).
func HealthHandler(build BuildInfo, checkDB bool, ping ...func() error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := http.StatusOK
		body := map[string]any{"status": "ok", "version": build.Version, "commit": build.Commit}
		if checkDB {
			for _, p := range ping {
				if err := p(); err != nil {
					status = http.StatusServiceUnavailable
					body["status"] = "degraded"
					body["db"] = err.Error()
				}
			}
			if _, ok := body["db"]; !ok {
				body["db"] = "ok"
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
}

// SecurityHeaders applies baseline hardening headers (Phase 10 extends with rate limits).
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}
