package ldap

import (
	"encoding/json"
	"net/http"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

// Deps wires handlers to the sync service.
type Deps struct {
	Svc *Service
	DB  platform.DBTX
}

// Middleware builds Require-style RBAC gates (identity.Handler.Require in production).
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the ldap surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("ldap", "user", "read")).Get("/ldap/status", h.Status)
	r.With(mw("ldap", "user", "write")).Post("/ldap/sync", h.Sync)
	r.With(mw("ldap", "user", "read")).Get("/ldap/users", h.List)
}

// Handler implements the ldap HTTP surface.
type Handler struct{ deps Deps }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// Status reports whether directory sync is configured (never leaks the URL).
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": h.deps.Svc.Config.Enabled()})
}

// Sync pulls directory entries into the local cache.
func (h *Handler) Sync(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	res, err := h.deps.Svc.Sync(r.Context(), h.deps.DB, entityID)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// List returns mirrored entries for the caller's entity.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	list, err := h.deps.Svc.Store.ListUsers(r.Context(), h.deps.DB, entityID)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}
