package assets

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

// Deps wires handlers to persistence and the event bus.
type Deps struct {
	Store Store
	Bus   platform.Bus
	DB    platform.DBTX
}

// Middleware builds Require-style RBAC gates (identity.Handler.Require in production).
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the assets surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("assets", "asset", "write")).Post("/assets", h.CreateAsset)
	r.With(mw("assets", "asset", "read")).Get("/assets", h.ListAssets)
	r.With(mw("assets", "asset", "write")).Put("/assets/{id}", h.UpdateAsset)
	r.With(mw("assets", "asset", "validate")).Post("/assets/{id}/status", h.SetAssetStatus)
}

// Handler implements the assets HTTP surface.
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

func (h *Handler) publish(ctx context.Context, entityID int64, subject, entity string, id int64) {
	if h.deps.Bus == nil {
		return
	}
	_ = h.deps.Bus.Publish(ctx, platform.Event{Subject: subject, Entity: entity, EntityID: entityID, ID: id})
}

type statusIn struct {
	Status     int16 `json:"status"`
	RowVersion int64 `json:"row_version"`
}

// CreateAsset registers an in-service asset.
func (h *Handler) CreateAsset(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var a Asset
	if err := decode(r, &a); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	a.ID = 0
	a.EntityID = entityID
	a.Status = AssetInService
	if err := h.deps.Store.CreateAsset(r.Context(), h.deps.DB, &a); err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.assets.created.v1", "asset", a.ID)
	writeJSON(w, http.StatusCreated, a)
}

type updateAssetIn struct {
	Label       string `json:"label"`
	Serial      string `json:"serial"`
	WarehouseID *int64 `json:"warehouse_id"`
	RowVersion  int64  `json:"row_version"`
}

// UpdateAsset edits a non-retired asset.
func (h *Handler) UpdateAsset(w http.ResponseWriter, r *http.Request) {
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
	var in updateAssetIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	a, err := h.deps.Store.UpdateAsset(r.Context(), h.deps.DB, entityID, id, in.Label, in.Serial, in.WarehouseID, in.RowVersion)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// ListAssets pages assets.
func (h *Handler) ListAssets(w http.ResponseWriter, r *http.Request) {
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
	list, err := h.deps.Store.ListAssets(r.Context(), h.deps.DB, entityID, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// SetAssetStatus moves an asset along its lifecycle.
func (h *Handler) SetAssetStatus(w http.ResponseWriter, r *http.Request) {
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
	var in statusIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	a, err := h.deps.Store.SetAssetStatus(r.Context(), h.deps.DB, entityID, id, AssetStatus(in.Status), in.RowVersion)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}
