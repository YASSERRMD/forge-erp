package module

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Deps wires the registry endpoint to persistence.
// Registry is the process catalogue; Store holds per-entity flags; DB is the
// pool-backed handle handlers pass through (services pass their tx).
type Deps struct {
	Registry *Registry
	Store    Store
	DB       platform.DBTX
}

// Routes mounts the registry surface (caller nests at /api/v1):
// GET /modules — the SPA bootstrap list (name/family/enabled/rights).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("module", "module", "read")).Get("/modules", h.List)
}

// Handler implements the registry HTTP surface.
type Handler struct{ deps Deps }

// List serves ListModules as JSON (401 without a resolvable tenant).
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	entityID, err := platform.EntityOf(r)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	infos, err := ListModules(r.Context(), h.deps.DB, h.deps.Store, h.deps.Registry, entityID)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	if infos == nil {
		infos = []ModuleInfo{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(infos)
}
