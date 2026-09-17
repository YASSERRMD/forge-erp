package collab

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

// Routes mounts the collab surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("collab", "comment", "write")).Post("/comments", h.Add)
	r.With(mw("collab", "comment", "read")).Get("/comments", h.List)
	r.With(mw("collab", "comment", "write")).Delete("/comments/{id}", h.Remove)
}

// Handler implements the collab HTTP surface.
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
	if u := r.URL.Query().Get("author"); u != "" {
		return u
	}
	return "me"
}

// Add persists one comment (author=? defaults the author login).
func (h *Handler) Add(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var c Comment
	if err := decode(r, &c); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	c.ID = 0
	c.EntityID = entityID
	if c.Author == "" {
		c.Author = userOf(r)
	}
	if err := h.deps.Svc.Add(r.Context(), h.deps.DB, &c); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

// List returns comments on one object (scope, object_type, object_id query;
// thread narrows, limit/offset page).
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	q := r.URL.Query()
	objectID, _ := strconv.ParseInt(q.Get("object_id"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	list, err := h.deps.Svc.Store.ListForObject(r.Context(), h.deps.DB, entityID,
		q.Get("scope"), q.Get("object_type"), objectID, q.Get("thread"), limit, offset)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// Remove deletes one author-owned comment.
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
	if err := h.deps.Svc.Remove(r.Context(), h.deps.DB, entityID, id, userOf(r)); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
