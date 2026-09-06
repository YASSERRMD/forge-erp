package partners

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func passthrough(_ string, _ string, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

func testRouter() (http.Handler, *MemoryStore) {
	st := NewMemoryStore()
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { Routes(r, Deps{Store: st}, passthrough) })
	return r, st
}

func post(t *testing.T, h http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestOrgLifecycle(t *testing.T) {
	h, _ := testRouter()

	rec := post(t, h, "/api/v1/organizations", map[string]any{
		"name": "Acme", "is_customer": true, "customer_code": "AC-1",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var created Organization
	_ = json.NewDecoder(rec.Body).Decode(&created)

	// Duplicate code → 409.
	rec = post(t, h, "/api/v1/organizations", map[string]any{
		"name": "Acme 2", "is_customer": true, "customer_code": "AC-1",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate: code=%d want 409", rec.Code)
	}

	// Role-less org → 422.
	rec = post(t, h, "/api/v1/organizations", map[string]any{"name": "Nobody"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("role-less: code=%d want 422", rec.Code)
	}

	// Get → 200; unknown → 404.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/1", nil)
	get := httptest.NewRecorder()
	h.ServeHTTP(get, req)
	if get.Code != http.StatusOK {
		t.Fatalf("get: code=%d", get.Code)
	}
	req404 := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/9999", nil)
	rec404 := httptest.NewRecorder()
	h.ServeHTTP(rec404, req404)
	if rec404.Code != http.StatusNotFound {
		t.Fatalf("get missing: code=%d want 404", rec404.Code)
	}

	// Contact lifecycle.
	crec := post(t, h, "/api/v1/organizations/1/contacts", map[string]any{
		"first_name": "Ada", "role": "billing",
	})
	if crec.Code != http.StatusCreated {
		t.Fatalf("create contact: code=%d body=%s", crec.Code, crec.Body.String())
	}
	crecBad := post(t, h, "/api/v1/organizations/9999/contacts", map[string]any{"first_name": "Ghost"})
	if crecBad.Code != http.StatusNotFound {
		t.Fatalf("orphan contact: code=%d want 404", crecBad.Code)
	}
}

func TestOrgHierarchyCycleRejected(t *testing.T) {
	h, st := testRouter()
	_ = st
	// Root org.
	rec := post(t, h, "/api/v1/organizations", map[string]any{"name": "Root", "is_customer": true})
	var root Organization
	_ = json.NewDecoder(rec.Body).Decode(&root)
	// Child.
	rec2 := post(t, h, "/api/v1/organizations", map[string]any{
		"name": "Child", "is_supplier": true, "parent_id": root.ID,
	})
	if rec2.Code != http.StatusCreated {
		t.Fatalf("child: code=%d body=%s", rec2.Code, rec2.Body.String())
	}
	// Self-parent on create (ID unknown → use update path with its own id).
	var child Organization
	_ = json.NewDecoder(rec2.Body).Decode(&child)
	raw, _ := json.Marshal(map[string]any{
		"name": "Child", "is_supplier": true, "parent_id": child.ID, "row_version": child.RowVersion,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/organizations/2", bytes.NewReader(raw))
	up := httptest.NewRecorder()
	h.ServeHTTP(up, req)
	if up.Code != http.StatusUnprocessableEntity {
		t.Fatalf("self-parent update: code=%d want 422 body=%s", up.Code, up.Body.String())
	}
}
