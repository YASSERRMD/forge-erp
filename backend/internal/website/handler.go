package website

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

// Deps wires handlers to the service layer.
type Deps struct {
	Svc *Service
	DB  platform.DBTX
}

// Middleware builds Require-style RBAC gates (identity.Handler.Require in production).
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the website surface (caller nests at /api/v1). Read-only:
// the static site consumes published content; authoring stays in kb.
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("website", "page", "read")).Get("/website/pages", h.List)
	r.With(mw("website", "page", "read")).Get("/website/pages/{slug}", h.Get)
	r.With(mw("website", "page", "read")).Get("/website/sitemap", h.Sitemap)
}

// Handler implements the website HTTP surface.
type Handler struct{ deps Deps }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// List returns published pages, newest first.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	list, err := h.deps.Svc.Store.ListPages(r.Context(), h.deps.DB, entityID, limit, offset)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// Get returns one published page by slug.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	page, err := h.deps.Svc.Page(r.Context(), entityID, chi.URLParam(r, "slug"))
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// Sitemap returns the page index for the static site.
func (h *Handler) Sitemap(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	entries, err := h.deps.Svc.Sitemap(r.Context(), entityID)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entries)
}
