package quickmemo

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

// Routes mounts the quickmemo surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("quickmemo", "memo", "write")).Post("/memos", h.Create)
	r.With(mw("quickmemo", "memo", "read")).Get("/memos", h.List)
	r.With(mw("quickmemo", "memo", "read")).Get("/memos/{id}", h.Get)
	r.With(mw("quickmemo", "memo", "write")).Put("/memos/{id}", h.Update)
	r.With(mw("quickmemo", "memo", "write")).Delete("/memos/{id}", h.Delete)
}

// Handler implements the quickmemo HTTP surface.
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

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// Create persists one memo for the caller.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var m Memo
	if err := decode(r, &m); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	m.ID = 0
	m.EntityID = entityID
	if m.UserLogin == "" {
		m.UserLogin = userOf(r)
	}
	if err := h.deps.Svc.Create(r.Context(), h.deps.DB, &m); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

// List pages the caller's memos (user=? selects the login).
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	list, err := h.deps.Svc.Store.ListForUser(r.Context(), h.deps.DB, entityID, userOf(r), 50, 0)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// Get returns one memo owned by the caller.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	id, ok := pathID(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	m, err := h.deps.Svc.Store.MemoByID(r.Context(), h.deps.DB, entityID, id, userOf(r))
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

type updateIn struct {
	Title      string `json:"title"`
	Body       string `json:"body"`
	RowVersion int64  `json:"row_version"`
}

// Update edits one memo with an optimistic-locking guard.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	id, ok := pathID(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var in updateIn
	if err := decode(r, &in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	m, err := h.deps.Svc.Update(r.Context(), h.deps.DB, entityID, id, userOf(r), in.Title, in.Body, in.RowVersion)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// Delete removes one memo owned by the caller.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	id, ok := pathID(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	if err := h.deps.Svc.Delete(r.Context(), h.deps.DB, entityID, id, userOf(r)); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
