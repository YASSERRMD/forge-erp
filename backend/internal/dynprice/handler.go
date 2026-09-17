package dynprice

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

// Routes mounts the dynprice surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("dynprice", "rule", "write")).Post("/price-rules", h.CreateRule)
	r.With(mw("dynprice", "rule", "read")).Get("/price-rules", h.ListRules)
	r.With(mw("dynprice", "rule", "read")).Get("/price-rules/{id}", h.GetRule)
	r.With(mw("dynprice", "rule", "write")).Put("/price-rules/{id}", h.UpdateRule)
	r.With(mw("dynprice", "rule", "write")).Delete("/price-rules/{id}", h.DeleteRule)
	r.With(mw("dynprice", "rule", "read")).Post("/price-rules/{id}/evaluate", h.Evaluate)
	r.With(mw("dynprice", "rule", "write")).Post("/price-assignments", h.Assign)
	r.With(mw("dynprice", "rule", "write")).Delete("/price-assignments/{id}", h.Unassign)
}

// Handler implements the dynprice HTTP surface.
type Handler struct{ deps Deps }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func decode(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v)
}

func pathID(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// CreateRule creates a price expression rule.
func (h *Handler) CreateRule(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var rule Rule
	if err := decode(r, &rule); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	rule.ID = 0
	rule.EntityID = entityID
	rule.Status = RuleActive // creates are active; archive via PUT
	if err := h.deps.Svc.CreateRule(r.Context(), &rule); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

// ListRules pages rules within the caller's entity.
func (h *Handler) ListRules(w http.ResponseWriter, r *http.Request) {
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
	list, err := h.deps.Svc.Store.ListRules(r.Context(), h.deps.DB, entityID, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// GetRule fetches one rule (404 outside the caller's entity).
func (h *Handler) GetRule(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	rule, err := h.deps.Svc.Store.RuleByID(r.Context(), h.deps.DB, entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

// UpdateRule edits a rule with optimistic locking (409 on stale row_version).
func (h *Handler) UpdateRule(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var rule Rule
	if err := decode(r, &rule); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	rule.ID = id
	rule.EntityID = entityID
	if err := h.deps.Svc.Store.UpdateRule(r.Context(), h.deps.DB, &rule); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

// DeleteRule removes a rule (assignments cascade).
func (h *Handler) DeleteRule(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := h.deps.Svc.Store.DeleteRule(r.Context(), h.deps.DB, entityID, id); err != nil {
		platform.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type evalOut struct {
	Price int64 `json:"price"`
}

// Evaluate runs one rule over the posted base/qty/cost.
func (h *Handler) Evaluate(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var in Input
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	price, err := h.deps.Svc.EvaluateRule(r.Context(), entityID, id, in)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, evalOut{Price: price})
}

// Assign binds a rule to a product/org.
func (h *Handler) Assign(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var a Assignment
	if err := decode(r, &a); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	a.ID = 0
	a.EntityID = entityID
	if err := h.deps.Svc.Store.Assign(r.Context(), h.deps.DB, &a); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

// Unassign removes one assignment.
func (h *Handler) Unassign(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := h.deps.Svc.Store.Unassign(r.Context(), h.deps.DB, entityID, id); err != nil {
		platform.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
