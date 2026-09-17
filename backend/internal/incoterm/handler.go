package incoterm

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Deps wires handlers to the code table.
type Deps struct {
	Store Store
	DB    platform.DBTX
}

// Middleware builds Require-style RBAC gates.
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the incoterm surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{svc: NewService(d.Store, d.DB)}
	r.With(mw("sales", "incoterm", "read")).Get("/incoterms", h.List)
	r.With(mw("sales", "incoterm", "read")).Get("/incoterms/{code}", h.Get)
}

// Handler implements the incoterm HTTP surface.
type Handler struct{ svc *Service }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// List returns the seeded Incoterms 2020 table.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	terms, err := h.svc.List(r.Context())
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, terms)
}

// Get fetches one term (unknown codes 404).
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	t, err := h.svc.Get(r.Context(), chi.URLParam(r, "code"))
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}
