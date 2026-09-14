// Package platform error kernel (Phase 0 task 8): shared sentinel errors,
// tenant resolution, and HTTP error mapping for all bounded contexts.
//
// Every context's handler maps store errors with ErrorCode (errors.Is only —
// never strings.Contains on raw messages) and renders them with WriteError
// (generic body for unknown errors; the detail is logged, never sent).
package platform

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/jackc/pgx/v5/pgconn"
)

// Shared sentinel errors. Context packages must return (or wrap with %w)
// these values so errors.Is works across package boundaries:
//   - ErrNotFound: row missing or outside the caller's entity (→ 404)
//   - ErrConflict / ErrAlreadyExists: duplicate / reconciled (→ 409)
//   - ErrVersionConflict: optimistic-locking mismatch (→ 409)
//   - ErrValidation: business-rule rejection (→ 422)
//   - ErrUnauthorized: no tenant resolvable / bad credentials (→ 401)
//
// identity.ErrNotFound, identity.ErrVersionConflict and finance.ErrNotFound
// are aliases of these values (see those packages' store.go); errors.Is
// matches regardless of which package's name the caller uses.
var (
	ErrNotFound        = errors.New("platform: not found")
	ErrConflict        = errors.New("platform: conflict")
	ErrAlreadyExists   = ErrConflict
	ErrValidation      = errors.New("platform: validation failed")
	ErrVersionConflict = errors.New("platform: row version conflict")
	ErrUnauthorized    = errors.New("platform: unauthorized")
)

// ErrorCode maps err to an HTTP status using errors.Is only. Unknown errors
// (including raw driver failures) map to 500. nil maps to 200 so callers can
// pass through directly; WriteError treats nil as a programming error (500).
func ErrorCode(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrUnauthorized):
		return http.StatusUnauthorized
	case errors.Is(err, ErrConflict):
		return http.StatusConflict
	case errors.Is(err, ErrVersionConflict):
		return http.StatusConflict
	case errors.Is(err, ErrValidation):
		return http.StatusUnprocessableEntity
	default:
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return http.StatusConflict // unique violation without a wrapped sentinel
		}
		return http.StatusInternalServerError
	}
}

// entityKey carries the resolved tenant for EntityOf. identity's Require
// middleware stores the authenticated user's entity under this key (in
// addition to its own user context), so EntityOf never imports identity
// (which would be an import cycle: identity already imports platform).
type entityKey struct{}

// ContextWithEntity returns ctx carrying entityID for EntityOf. Production
// middleware (identity Require) sets this; tests inject it directly to
// simulate an authenticated tenant.
func ContextWithEntity(ctx context.Context, entityID int64) context.Context {
	return context.WithValue(ctx, entityKey{}, entityID)
}

// EntityOf resolves the caller's tenant. It NEVER defaults to entity 1:
// requests without a resolvable tenant get (0, ErrUnauthorized) and handlers
// must answer 401 via WriteError.
func EntityOf(r *http.Request) (int64, error) {
	if v, ok := r.Context().Value(entityKey{}).(int64); ok && v != 0 {
		return v, nil
	}
	return 0, ErrUnauthorized
}

// WriteError renders err as {"error": msg}. Known errors keep their detail;
// unknown errors get a generic "internal error" body while the detail goes
// to the log — raw error text is never sent to the client for 500s.
func WriteError(w http.ResponseWriter, err error) {
	code := ErrorCode(err)
	msg := "internal error"
	switch code {
	case http.StatusNotFound:
		msg = "not found"
		if err != nil {
			msg = err.Error()
		}
	case http.StatusConflict:
		msg = "conflict"
		if err != nil {
			msg = err.Error()
		}
	case http.StatusUnprocessableEntity:
		msg = "validation failed"
		if err != nil {
			msg = err.Error()
		}
	case http.StatusUnauthorized:
		msg = "unauthorized"
	case http.StatusOK:
		msg = "ok"
	}
	if code == http.StatusInternalServerError {
		log.Printf("platform: internal error: %v", err)
	}
	if err == nil {
		code = http.StatusInternalServerError
		msg = "internal error"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
