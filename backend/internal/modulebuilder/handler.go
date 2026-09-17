package modulebuilder

import (
	"encoding/json"
	"net/http"

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

// Routes mounts the modulebuilder surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("modulebuilder", "module", "write")).Post("/modulebuilder/scaffold", h.Scaffold)
	r.With(mw("modulebuilder", "module", "read")).Post("/modulebuilder/validate", h.Validate)
	r.With(mw("modulebuilder", "module", "write")).Post("/modulebuilder/install", h.Install)
	r.With(mw("modulebuilder", "module", "write")).Post("/modulebuilder/uninstall", h.Uninstall)
	r.With(mw("modulebuilder", "module", "read")).Get("/modulebuilder/modules", h.List)
}

// Handler implements the modulebuilder HTTP surface.
type Handler struct{ deps Deps }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func decode(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v)
}

// Scaffold generates the 4-file skeleton + migration stub + routes snippet.
func (h *Handler) Scaffold(w http.ResponseWriter, r *http.Request) {
	var spec ScaffoldSpec
	if err := decode(r, &spec); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	files, err := h.deps.Svc.Scaffold(r.Context(), spec)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files, "keys": ScaffoldKeys(files)})
}

// Validate structurally checks a manifest (no writes).
func (h *Handler) Validate(w http.ResponseWriter, r *http.Request) {
	var m Manifest
	if err := decode(r, &m); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	if err := h.deps.Svc.Validate(r.Context(), m); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"valid": true})
}

type installIn struct {
	EntityID int64    `json:"entity_id"`
	Manifest Manifest `json:"manifest"`
}

// Install records activation in ferp_modules.
func (h *Handler) Install(w http.ResponseWriter, r *http.Request) {
	var in installIn
	if err := decode(r, &in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	if in.EntityID == 0 {
		if id, err := platform.EntityOf(r); err == nil {
			in.EntityID = id
		}
	}
	if err := h.deps.Svc.Install(r.Context(), h.deps.DB, in.EntityID, in.Manifest); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"installed": true})
}

type uninstallIn struct {
	EntityID int64  `json:"entity_id"`
	Name     string `json:"name"`
	Version  string `json:"version"`
}

// Uninstall records deactivation in ferp_modules.
func (h *Handler) Uninstall(w http.ResponseWriter, r *http.Request) {
	var in uninstallIn
	if err := decode(r, &in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	if in.EntityID == 0 {
		if id, err := platform.EntityOf(r); err == nil {
			in.EntityID = id
		}
	}
	if err := h.deps.Svc.Uninstall(r.Context(), h.deps.DB, in.EntityID, in.Name, in.Version); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"uninstalled": true})
}

// List returns activation rows for the caller's entity.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	list, err := h.deps.Svc.Store.List(r.Context(), h.deps.DB, entityID)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}
