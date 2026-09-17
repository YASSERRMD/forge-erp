package stocktransfer

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

// Routes mounts the stocktransfer surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("stocktransfer", "transfer", "write")).Post("/stock-transfers", h.Create)
	r.With(mw("stocktransfer", "transfer", "read")).Get("/stock-transfers", h.List)
	r.With(mw("stocktransfer", "transfer", "read")).Get("/stock-transfers/{id}", h.Get)
	r.With(mw("stocktransfer", "transfer", "write")).Put("/stock-transfers/{id}", h.Update)
	r.With(mw("stocktransfer", "transfer", "write")).Delete("/stock-transfers/{id}", h.Delete)
	r.With(mw("stocktransfer", "transfer", "write")).Post("/stock-transfers/{id}/lines", h.AddLine)
	r.With(mw("stocktransfer", "transfer", "read")).Get("/stock-transfers/{id}/lines", h.Lines)
	r.With(mw("stocktransfer", "transfer", "write")).Post("/stock-transfers/{id}/validate", h.Validate)
	r.With(mw("stocktransfer", "transfer", "write")).Post("/stock-transfers/{id}/cancel", h.Cancel)
}

// Handler implements the stocktransfer HTTP surface.
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

func (h *Handler) svc() *Service {
	if h.deps.Svc != nil {
		return h.deps.Svc
	}
	return &Service{Store: NewMemoryStore(), Ledger: nil, DB: h.deps.DB}
}

// Create opens a draft transfer (reference minted server-side).
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var t Transfer
	if err := decode(r, &t); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	t.ID = 0
	t.Ref = ""
	t.EntityID = entityID
	if err := h.svc().Create(r.Context(), &t); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

// List pages transfers within the caller's entity.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
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
	list, err := h.deps.Svc.Store.List(r.Context(), h.deps.DB, entityID, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// Get fetches one transfer (404 outside the caller's entity).
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
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
	t, err := h.deps.Svc.Store.TransferByID(r.Context(), h.deps.DB, entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// Update edits a draft (409 on stale row_version, 422 past draft).
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
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
	var t Transfer
	if err := decode(r, &t); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	t.ID = id
	t.EntityID = entityID
	if err := h.deps.Svc.Store.UpdateDraft(r.Context(), h.deps.DB, &t); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// Delete removes a draft transfer.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
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
	if err := h.deps.Svc.Store.DeleteDraft(r.Context(), h.deps.DB, entityID, id); err != nil {
		platform.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AddLine appends one product line to a draft.
func (h *Handler) AddLine(w http.ResponseWriter, r *http.Request) {
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
	var l TransferLine
	if err := decode(r, &l); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	l.ID = 0
	if err := h.deps.Svc.Store.AddLine(r.Context(), h.deps.DB, entityID, id, &l); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, l)
}

// Lines returns a transfer's lines in position order.
func (h *Handler) Lines(w http.ResponseWriter, r *http.Request) {
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
	lines, err := h.deps.Svc.Store.LinesOf(r.Context(), h.deps.DB, entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, lines)
}

// Validate posts the paired movements and flips draft→validated.
func (h *Handler) Validate(w http.ResponseWriter, r *http.Request) {
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
	done, err := h.svc().ValidateTransfer(r.Context(), entityID, id, nil)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, done)
}

// Cancel flips draft→canceled (validated transfers are terminal).
func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
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
	cur, err := h.deps.Svc.Store.TransferByID(r.Context(), h.deps.DB, entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	done, err := h.deps.Svc.Store.SetStatus(r.Context(), h.deps.DB, entityID, id, StatusCanceled, cur.RowVersion)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, done)
}
