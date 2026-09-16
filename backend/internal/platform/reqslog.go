// Package platform request-scoped structured logging (Phase 2 operational):
// slog with request correlation IDs (chi RequestID middleware, already
// installed in Router) and configurable sinks (stderr default, file via
// FERP_LOG_FILE). New code (portal, cron) logs through here; the older
// log.Printf call sites across other contexts are intentionally untouched
// (follow-up: migrate them package by package).
package platform

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"

	"github.com/go-chi/chi/v5/middleware"
)

var (
	loggerOnce sync.Once
	defaultLog *slog.Logger
)

// NewLogger builds the process logger. Sink selection:
//   - FERP_LOG_FILE set: append to that file (created 0600 when missing).
//   - otherwise: stderr.
// Level selection: FERP_LOG_LEVEL=debug enables debug, anything else info.
// JSON handler is used so log aggregators (syslog forwarders, Loki, …) can
// parse fields; request_id threads through via LoggerWith / ReqLogger.
func NewLogger() *slog.Logger {
	level := slog.LevelInfo
	if os.Getenv("FERP_LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	}
	var w io.Writer = os.Stderr
	if path := os.Getenv("FERP_LOG_FILE"); path != "" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			// Fail open on stderr: never crash boot because a log file is
			// unwritable; the fallback itself is recorded once usable.
			slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})).Error(
				"log file unreachable, falling back to stderr", "path", path, "error", err)
		} else {
			w = f
		}
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}

// Default returns the process logger, built once from the environment.
func Default() *slog.Logger {
	loggerOnce.Do(func() { defaultLog = NewLogger() })
	return defaultLog
}

// RequestIDOf extracts the chi RequestID for correlation. Empty when the
// request did not pass through Router (e.g. unit tests without middleware).
func RequestIDOf(r *http.Request) string {
	return middleware.GetReqID(r.Context())
}

// ReqLogger returns a logger carrying the request's correlation ID
// (request_id="") when no ID is present, so call sites stay uniform.
func ReqLogger(r *http.Request) *slog.Logger {
	return ReqCtxLogger(r.Context(), Default())
}

// ReqCtxLogger attaches the correlation ID from ctx (set by chi's RequestID
// middleware) to base. Prefer ReqLogger in handlers; use this in services
// that only have a context.
func ReqCtxLogger(ctx context.Context, base *slog.Logger) *slog.Logger {
	if base == nil {
		base = Default()
	}
	return base.With("request_id", middleware.GetReqID(ctx))
}
