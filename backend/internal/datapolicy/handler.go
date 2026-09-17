package datapolicy

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

// Deps wires handlers to persistence and the retention service.
type Deps struct {
	Store Store
	Svc   *Service
	Bus   platform.Bus
	DB    platform.DBTX
}

// Middleware builds Require-style RBAC gates (identity.Handler.Require in production).
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the datapolicy surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("datapolicy", "rule", "write")).Put("/data-policy/rules", h.UpsertRule)
	r.With(mw("datapolicy", "rule", "read")).Get("/data-policy/rules", h.ListRules)
	r.With(mw("datapolicy", "rule", "read")).Get("/data-policy/dry-run", h.DryRun)
	r.With(mw("datapolicy", "erasure", "write")).Post("/data-policy/erasures", h.RequestErasure)
	r.With(mw("datapolicy", "erasure", "read")).Get("/data-policy/erasures", h.ListErasures)
	r.With(mw("datapolicy", "erasure", "write")).Post("/data-policy/erasures/{id}/done", h.CompleteErasure)
}

// Handler implements the datapolicy HTTP surface.
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

func (h *Handler) svc() *Service {
	if h.deps.Svc != nil {
		return h.deps.Svc
	}
	return &Service{Store: h.deps.Store, Bus: h.deps.Bus, DB: h.deps.DB}
}

// UpsertRule creates or replaces the retention rule for a scope.
func (h *Handler) UpsertRule(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var rule RetentionRule
	if err := decode(r, &rule); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	rule.EntityID = entityID
	if err := h.deps.Store.UpsertRule(r.Context(), h.deps.DB, &rule); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

// ListRules lists the entity's retention rules.
func (h *Handler) ListRules(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	list, err := h.deps.Store.RulesOf(r.Context(), h.deps.DB, entityID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// DryRun serves the anonymisation dry-run report (read-only, ?scope=orgs|members).
func (h *Handler) DryRun(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = ScopeOrgs
	}
	rep, err := h.svc().DryRun(r.Context(), entityID, scope)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

type erasureIn struct {
	Scope     string `json:"scope"`
	SubjectID int64  `json:"subject_id"`
	Reason    string `json:"reason"`
}

// RequestErasure logs an erasure request.
func (h *Handler) RequestErasure(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var in erasureIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	e, err := h.svc().RequestErasure(r.Context(), entityID, in.Scope, in.SubjectID, in.Reason)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, e)
}

// ListErasures pages the erasure request log.
func (h *Handler) ListErasures(w http.ResponseWriter, r *http.Request) {
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
	list, err := h.deps.Store.ListErasures(r.Context(), h.deps.DB, entityID, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// CompleteErasure marks a pending erasure request done.
func (h *Handler) CompleteErasure(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	e, err := h.svc().CompleteErasure(r.Context(), entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}
