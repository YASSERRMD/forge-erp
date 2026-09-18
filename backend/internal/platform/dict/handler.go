package dict

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Deps wires handlers to the dictionary store.
type Deps struct {
	Store Store
	DB    platform.DBTX
}

// Middleware builds Require-style RBAC gates (identity.Handler.Require in production).
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the dictionary read surface (caller nests at /api/v1).
// Reference data is read-only over HTTP; seeding is migration-owned and
// admin edits stay out of scope (core entries deactivate, never delete).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("dict", "dictionary", "read")).Get("/dictionaries", h.ListDictionaries)
	r.With(mw("dict", "dictionary", "read")).Get("/dictionaries/{code}", h.ListEntries)
}

// Handler implements the dictionary HTTP surface.
type Handler struct{ deps Deps }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// ListDictionaries returns every reference list.
func (h *Handler) ListDictionaries(w http.ResponseWriter, r *http.Request) {
	list, err := h.deps.Store.ListDictionaries(r.Context(), h.deps.DB)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// ListEntries returns one dictionary's entries with locale-resolved labels
// (?locale=fr resolves overrides; ?active=0 includes deactivated rows).
func (h *Handler) ListEntries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	activeOnly := q.Get("active") != "0"
	entries, err := h.deps.Store.List(r.Context(), h.deps.DB,
		chi.URLParam(r, "code"), q.Get("locale"), activeOnly)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entries)
}
