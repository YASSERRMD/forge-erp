package sepa

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Deps wires handlers to persistence and the event bus.
type Deps struct {
	Store Store
	Bus   platform.Bus
}

// Middleware builds Require-style RBAC gates (identity.Handler.Require in production).
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the sepa surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("sepa", "batch", "write")).Post("/sepa/batches", h.CreateBatch)
	r.With(mw("sepa", "batch", "read")).Get("/sepa/batches", h.ListBatches)
	r.With(mw("sepa", "batch", "validate")).Post("/sepa/batches/{id}/status", h.SetBatchStatus)
	r.With(mw("sepa", "batch", "read")).Get("/sepa/batches/{id}/xml", h.ExportXML)
}

// Handler implements the sepa HTTP surface.
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

func entityOf(r *http.Request) int64 {
	if u, ok := identity.AuthUser(r); ok && u.EntityID != 0 {
		return u.EntityID
	}
	return 1
}

func storeErrorCode(err error) int {
	switch {
	case errors.Is(err, identity.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, identity.ErrVersionConflict):
		return http.StatusConflict
	case err != nil && strings.Contains(err.Error(), "duplicate"):
		return http.StatusConflict
	default:
		return http.StatusUnprocessableEntity
	}
}

func pathID(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func (h *Handler) publish(ctx context.Context, subject, entity string, id int64) {
	if h.deps.Bus == nil {
		return
	}
	_ = h.deps.Bus.Publish(ctx, platform.Event{Subject: subject, Entity: entity, ID: id})
}

type statusIn struct {
	Status     int16 `json:"status"`
	RowVersion int64 `json:"row_version"`
}

// CreateBatch opens a draft collection batch (IBANs validated).
func (h *Handler) CreateBatch(w http.ResponseWriter, r *http.Request) {
	var b Batch
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	b.ID = 0
	b.EntityID = entityOf(r)
	b.Status = BatchDraft
	if err := h.deps.Store.CreateBatch(r.Context(), &b); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, b)
}

// ListBatches lists batches.
func (h *Handler) ListBatches(w http.ResponseWriter, r *http.Request) {
	list, err := h.deps.Store.ListBatches(r.Context(), entityOf(r))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// SetBatchStatus moves a batch along its lifecycle.
func (h *Handler) SetBatchStatus(w http.ResponseWriter, r *http.Request) {
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
	b, err := h.deps.Store.SetBatchStatus(r.Context(), id, BatchStatus(in.Status), in.RowVersion)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	h.publish(r.Context(), "forgeerp.sepa.batch.status.v1", "batch", b.ID)
	writeJSON(w, http.StatusOK, b)
}

// ExportXML serves the pain.008 document for validated batches.
func (h *Handler) ExportXML(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	b, err := h.deps.Store.BatchByID(r.Context(), id)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	raw, err := ExportXML(b, time.Now().UTC())
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("Content-Disposition", `attachment; filename="`+b.Ref+`.xml"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}
