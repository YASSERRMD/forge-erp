package manufacturing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Deps wires handlers to persistence, the catalog ledger, and the event bus.
type Deps struct {
	Store  Store
	Ledger Ledger
	Bus    platform.Bus
}

// Middleware builds Require-style RBAC gates (identity.Handler.Require in production).
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the manufacturing surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("manufacturing", "bom", "write")).Post("/manufacturing/boms", h.CreateBOM)
	r.With(mw("manufacturing", "bom", "read")).Get("/manufacturing/boms", h.ListBOMs)
	r.With(mw("manufacturing", "bom", "read")).Get("/manufacturing/boms/{id}", h.GetBOM)
	r.With(mw("manufacturing", "bom", "validate")).Post("/manufacturing/boms/{id}/status", h.SetBOMStatus)
	r.With(mw("manufacturing", "bom", "write")).Post("/manufacturing/boms/{id}/lines", h.AddLine)
	r.With(mw("manufacturing", "bom", "read")).Get("/manufacturing/boms/{id}/lines", h.ListLines)
	r.With(mw("manufacturing", "mo", "write")).Post("/manufacturing/mos", h.CreateMO)
	r.With(mw("manufacturing", "mo", "read")).Get("/manufacturing/mos", h.ListMOs)
	r.With(mw("manufacturing", "mo", "read")).Get("/manufacturing/mos/{id}", h.GetMO)
	r.With(mw("manufacturing", "mo", "validate")).Post("/manufacturing/mos/{id}/status", h.SetMOStatus)
	r.With(mw("manufacturing", "mo", "produce")).Post("/manufacturing/mos/{id}/produce", h.Produce)
}

// Handler implements the manufacturing HTTP surface.
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
	case err != nil && strings.Contains(err.Error(), "not found"):
		return http.StatusNotFound
	case err != nil && strings.Contains(err.Error(), "conflict"):
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

// CreateBOM opens a draft bill of materials.
func (h *Handler) CreateBOM(w http.ResponseWriter, r *http.Request) {
	var b BOM
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	b.ID = 0
	b.EntityID = entityOf(r)
	b.Status = BOMDraft
	if err := h.deps.Store.CreateBOM(r.Context(), &b); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	h.publish(r.Context(), "forgeerp.manufacturing.bom.created.v1", "bom", b.ID)
	writeJSON(w, http.StatusCreated, b)
}

// ListBOMs pages BOMs within the caller's entity.
func (h *Handler) ListBOMs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	list, err := h.deps.Store.ListBOMs(r.Context(), entityOf(r), limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// GetBOM fetches one BOM.
func (h *Handler) GetBOM(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	b, err := h.deps.Store.BOMByID(r.Context(), id)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, b)
}

// SetBOMStatus moves a BOM along its state machine.
func (h *Handler) SetBOMStatus(w http.ResponseWriter, r *http.Request) {
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
	b, err := h.deps.Store.SetBOMStatus(r.Context(), id, BOMStatus(in.Status), in.RowVersion)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, b)
}

// AddLine appends a component line to a draft/active BOM.
func (h *Handler) AddLine(w http.ResponseWriter, r *http.Request) {
	bid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var l BOMLine
	if err := decode(r, &l); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	l.ID = 0
	l.EntityID = entityOf(r)
	l.BOMID = bid
	if err := h.deps.Store.AddLine(r.Context(), &l); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, l)
}

// ListLines lists a BOM's component lines.
func (h *Handler) ListLines(w http.ResponseWriter, r *http.Request) {
	bid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	list, err := h.deps.Store.LinesOf(r.Context(), bid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// CreateMO opens a draft manufacturing order.
func (h *Handler) CreateMO(w http.ResponseWriter, r *http.Request) {
	var mo ManufacturingOrder
	if err := decode(r, &mo); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	mo.ID = 0
	mo.EntityID = entityOf(r)
	mo.Status = MODraft
	if err := h.deps.Store.CreateMO(r.Context(), &mo); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	h.publish(r.Context(), "forgeerp.manufacturing.mo.created.v1", "mo", mo.ID)
	writeJSON(w, http.StatusCreated, mo)
}

// ListMOs pages manufacturing orders within the caller's entity.
func (h *Handler) ListMOs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	list, err := h.deps.Store.ListMOs(r.Context(), entityOf(r), limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// GetMO fetches one MO.
func (h *Handler) GetMO(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	mo, err := h.deps.Store.MOByID(r.Context(), id)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, mo)
}

// SetMOStatus moves an MO along its state machine.
func (h *Handler) SetMOStatus(w http.ResponseWriter, r *http.Request) {
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
	mo, err := h.deps.Store.SetMOStatus(r.Context(), id, MOStatus(in.Status), in.RowVersion)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, mo)
}

// Produce consumes components and receipts the finished good through the
// catalog ledger, then flips the MO to produced. 422 on insufficient stock.
func (h *Handler) Produce(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	mo, err := h.deps.Store.MOByID(r.Context(), id)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	lines, err := h.deps.Store.LinesOf(r.Context(), mo.BOMID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "lines failed")
		return
	}
	plan, err := PostProduce(r.Context(), mo, lines, h.deps.Ledger)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	done, err := h.deps.Store.MarkProduced(r.Context(), mo.ID, mo.RowVersion)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	h.publish(r.Context(), "forgeerp.manufacturing.mo.produced.v1", "mo", done.ID)
	writeJSON(w, http.StatusOK, map[string]any{"mo": done, "plan": plan})
}
