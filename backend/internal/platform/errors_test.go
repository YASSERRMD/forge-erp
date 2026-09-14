package platform

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ErrorCode maps sentinels via errors.Is, never via message substrings.
func TestErrorCodeSentinels(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{nil, http.StatusOK},
		{ErrNotFound, http.StatusNotFound},
		{ErrConflict, http.StatusConflict},
		{ErrAlreadyExists, http.StatusConflict},
		{ErrVersionConflict, http.StatusConflict},
		{ErrValidation, http.StatusUnprocessableEntity},
		{ErrUnauthorized, http.StatusUnauthorized},
		{fmt.Errorf("wrapped: %w", ErrNotFound), http.StatusNotFound},
		{fmt.Errorf("wrapped: %w", ErrConflict), http.StatusConflict},
		{fmt.Errorf("wrapped: %w", ErrValidation), http.StatusUnprocessableEntity},
		{fmt.Errorf("%w: %w", errors.New("domain detail"), ErrValidation), http.StatusUnprocessableEntity},
		{errors.New("someone: duplicate thing"), http.StatusInternalServerError},
		{errors.New("someone: not found"), http.StatusInternalServerError},
		{errors.New("boom"), http.StatusInternalServerError},
	}
	for _, c := range cases {
		if got := ErrorCode(c.err); got != c.want {
			t.Errorf("ErrorCode(%v)=%d want %d", c.err, got, c.want)
		}
	}
}

// EntityOf never defaults: no tenant resolvable means unauthorized.
func TestEntityOfNoDefault(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if _, err := EntityOf(r); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("bare request err=%v want ErrUnauthorized", err)
	}
	r = r.WithContext(ContextWithEntity(r.Context(), 0))
	if _, err := EntityOf(r); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("zero entity err=%v want ErrUnauthorized", err)
	}
	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2 = r2.WithContext(ContextWithEntity(r2.Context(), 7))
	id, err := EntityOf(r2)
	if err != nil || id != 7 {
		t.Fatalf("injected entity id=%d err=%v", id, err)
	}
}

// WriteError sends generic bodies for unknown errors (never err.Error()).
func TestWriteErrorGeneric500(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, errors.New("db exploded: secret detail"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code=%d want 500", rec.Code)
	}
	if body := rec.Body.String(); body != "{\"error\":\"internal error\"}\n" {
		t.Fatalf("body=%q want generic", body)
	}
	rec = httptest.NewRecorder()
	WriteError(rec, fmt.Errorf("thing missing: %w", ErrNotFound))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code=%d want 404", rec.Code)
	}
}
