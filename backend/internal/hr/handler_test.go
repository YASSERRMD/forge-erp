package hr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func passthrough(_, _, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

func testRouter() http.Handler {
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Store: NewMemoryStore()}, passthrough)
	})
	return r
}

func doReq(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestLeaveAPI(t *testing.T) {
	h := testRouter()
	rec := doReq(t, h, http.MethodPost, "/api/v1/hr/leaves", map[string]any{
		"user_login": "ada", "type": "paid",
		"start_date": "2026-09-06T00:00:00Z", "end_date": "2026-09-08T00:00:00Z",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create leave: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var l LeaveRequest
	_ = json.NewDecoder(rec.Body).Decode(&l)
	if l.Days != 3 {
		t.Fatalf("days=%d want 3", l.Days)
	}
	// Bad range → 422.
	rec = doReq(t, h, http.MethodPost, "/api/v1/hr/leaves", map[string]any{
		"user_login": "ada", "type": "paid",
		"start_date": "2026-09-08T00:00:00Z", "end_date": "2026-09-06T00:00:00Z",
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad range: code=%d want 422", rec.Code)
	}
	// Submit then approve.
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/hr/leaves/%d/status", l.ID),
		map[string]any{"status": 1, "row_version": l.RowVersion})
	if rec.Code != http.StatusOK {
		t.Fatalf("submit: code=%d body=%s", rec.Code, rec.Body.String())
	}
	_ = json.NewDecoder(rec.Body).Decode(&l)
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/hr/leaves/%d/status", l.ID),
		map[string]any{"status": 2, "row_version": l.RowVersion})
	if rec.Code != http.StatusOK {
		t.Fatalf("approve: code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestExpenseAPI(t *testing.T) {
	h := testRouter()
	rec := doReq(t, h, http.MethodPost, "/api/v1/hr/expenses",
		map[string]any{"ref": "EXP-9", "user_login": "ada"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var r ExpenseReport
	_ = json.NewDecoder(rec.Body).Decode(&r)
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/hr/expenses/%d/lines", r.ID),
		map[string]any{"date": "2026-09-06T00:00:00Z", "label": "taxi", "amount": 1500})
	if rec.Code != http.StatusCreated {
		t.Fatalf("add line: code=%d body=%s", rec.Code, rec.Body.String())
	}
	// Advance to approved, then line must be rejected.
	for _, st := range []int16{1, 2} {
		rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/hr/expenses/%d/status", r.ID),
			map[string]any{"status": st, "row_version": r.RowVersion})
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: code=%d", st, rec.Code)
		}
		_ = json.NewDecoder(rec.Body).Decode(&r)
	}
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/hr/expenses/%d/lines", r.ID),
		map[string]any{"date": "2026-09-06T00:00:00Z", "label": "late", "amount": 100})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("late line: code=%d want 422", rec.Code)
	}
}

func TestSalaryAPI(t *testing.T) {
	h := testRouter()
	// Unbalanced → 422.
	rec := doReq(t, h, http.MethodPost, "/api/v1/hr/salaries", map[string]any{
		"user_login": "ada", "period": "2026-09", "gross": 1000, "charges": 200, "net": 700,
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unbalanced: code=%d want 422", rec.Code)
	}
	rec = doReq(t, h, http.MethodPost, "/api/v1/hr/salaries", map[string]any{
		"user_login": "ada", "period": "2026-09", "gross": 1000, "charges": 200, "net": 800,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var s Salary
	_ = json.NewDecoder(rec.Body).Decode(&s)
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/hr/salaries/%d/status", s.ID),
		map[string]any{"status": 1, "row_version": s.RowVersion})
	if rec.Code != http.StatusOK {
		t.Fatalf("validate: code=%d body=%s", rec.Code, rec.Body.String())
	}
}
