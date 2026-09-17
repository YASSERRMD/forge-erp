package bookmark

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

// Routes mounts the bookmark surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("bookmark", "bookmark", "write")).Post("/bookmarks", h.Add)
	r.With(mw("bookmark", "bookmark", "write")).Post("/bookmarks/toggle", h.Toggle)
	r.With(mw("bookmark", "bookmark", "read")).Get("/bookmarks", h.List)
	r.With(mw("bookmark", "bookmark", "write")).Delete("/bookmarks/{id}", h.Remove)
}

// Handler implements the bookmark HTTP surface.
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

func userOf(r *http.Request) string {
	if u := r.URL.Query().Get("user"); u != "" {
		return u
	}
	return "me"
}

// Add persists one bookmark for the caller.
func (h *Handler) Add(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var b Bookmark
	if err := decode(r, &b); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	b.ID = 0
	b.EntityID = entityID
	if b.UserLogin == "" {
		b.UserLogin = userOf(r)
	}
	if err := h.deps.Svc.Add(r.Context(), h.deps.DB, &b); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, b)
}

type toggleOut struct {
	Added bool `json:"added"`
}

// Toggle flips bookmark membership idempotently.
func (h *Handler) Toggle(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var b Bookmark
	if err := decode(r, &b); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	b.EntityID = entityID
	if b.UserLogin == "" {
		b.UserLogin = userOf(r)
	}
	added, err := h.deps.Svc.Toggle(r.Context(), h.deps.DB, b)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toggleOut{Added: added})
}

// List returns the caller's bookmarks (user=? selects the login).
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	list, err := h.deps.Svc.Store.ListForUser(r.Context(), h.deps.DB, entityID, userOf(r))
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// Remove deletes one bookmark owned by the caller.
func (h *Handler) Remove(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	if err := h.deps.Svc.Store.Remove(r.Context(), h.deps.DB, entityID, id, userOf(r)); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
