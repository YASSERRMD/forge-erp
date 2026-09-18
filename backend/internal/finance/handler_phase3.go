// Phase 3 handlers: bindings, chart packs, auto-postings, close trail and
// FEC export. The poster and close log travel via Deps (nil = 501 "not
// configured" outside production wiring); route registration never touches
// them, so the OpenAPI walker stays database-free.
package finance

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// RoutesPhase3 mounts the statutory surface (called from Routes).
func RoutesPhase3(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("finance", "binding", "write")).Post("/finance/bindings", h.SetBinding)
	r.With(mw("finance", "binding", "read")).Get("/finance/bindings", h.ListBindings)
	r.With(mw("finance", "account", "write")).Post("/finance/charts/load", h.LoadChartPack)
	r.With(mw("finance", "entry", "write")).Post("/finance/postings/sales-invoices/{id}", h.PostSalesInvoice)
	r.With(mw("finance", "entry", "write")).Post("/finance/postings/supplier-invoices/{id}", h.PostSupplierInvoice)
	r.With(mw("finance", "entry", "read")).Get("/finance/postings", h.ListPostings)
	r.With(mw("finance", "entry", "write")).Post("/finance/fiscal-years/{id}/reopen", h.ReopenYear)
	r.With(mw("finance", "entry", "read")).Get("/finance/fiscal-years/{id}/close-log", h.CloseLog)
	r.With(mw("finance", "entry", "read")).Get("/finance/exports/fec", h.ExportFEC)
}

// bindings resolves the mapping store (explicit Deps first, PG assertion
// second — memory stores keep bindings in the separate fake).
func (h *Handler) bindings() (BindingStore, bool) {
	if h.deps.Bindings != nil {
		return h.deps.Bindings, true
	}
	store, ok := h.deps.Store.(BindingStore)
	return store, ok
}

// SetBinding creates or replaces one (kind, key) → account mapping.
func (h *Handler) SetBinding(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var b Binding
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	b.ID = 0
	b.EntityID = entityID
	store, ok := h.bindings()
	if !ok {
		writeErr(w, http.StatusNotImplemented, "bindings not configured")
		return
	}
	if err := store.SetBinding(r.Context(), h.deps.DB, &b); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, b)
}

// ListBindings lists bindings, optionally filtered by ?kind=.
func (h *Handler) ListBindings(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	store, ok := h.bindings()
	if !ok {
		writeErr(w, http.StatusNotImplemented, "bindings not configured")
		return
	}
	list, err := store.ListBindings(r.Context(), h.deps.DB, entityID, r.URL.Query().Get("kind"))
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type loadPackRequest struct {
	Pack string `json:"pack"` // FR, DE or US
}

// LoadChartPack loads a statutory chart for the caller's entity (idempotent).
func (h *Handler) LoadChartPack(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var req loadPackRequest
	if err := decode(r, &req); err != nil || req.Pack == "" {
		writeErr(w, http.StatusBadRequest, "pack required")
		return
	}
	loader, ok := h.deps.Store.(ChartPackLoader)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "chart packs require PostgreSQL")
		return
	}
	rep, err := loader.LoadChartPack(r.Context(), h.deps.DB, entityID, req.Pack)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// postInvoice runs the poster for one invoice family (idempotent replay).
func (h *Handler) postInvoice(w http.ResponseWriter, r *http.Request, docType string) {
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
	if h.deps.Poster == nil {
		writeErr(w, http.StatusNotImplemented, "auto-posting not configured")
		return
	}
	res, err := h.deps.Poster.PostValidated(r.Context(), entityID, docType, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// PostSalesInvoice posts one validated sales invoice (operator trigger;
// the event path calls the same poster).
func (h *Handler) PostSalesInvoice(w http.ResponseWriter, r *http.Request) {
	h.postInvoice(w, r, DocSalesInvoice)
}

// PostSupplierInvoice posts one validated supplier invoice.
func (h *Handler) PostSupplierInvoice(w http.ResponseWriter, r *http.Request) {
	h.postInvoice(w, r, DocSupplierInvoice)
}

// ListPostings pages idempotency rows newest first.
func (h *Handler) ListPostings(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	if h.deps.Poster == nil {
		writeErr(w, http.StatusNotImplemented, "auto-posting not configured")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	list, err := h.deps.Poster.Postings.ListPostings(r.Context(), h.deps.DB, entityID, limit, offset)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type reopenYearRequest struct {
	Note string `json:"note"`
}

// ReopenYear unlocks a locked year with an audit trail row.
func (h *Handler) ReopenYear(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	yearID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || yearID <= 0 {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var req reopenYearRequest
	_ = decode(r, &req) // empty body reopens without a note
	if err := h.svc().ReopenYear(r.Context(), h.deps.DB, ReopenCmd{
		EntityID: entityID, YearID: yearID, Note: req.Note}); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"reopened": true})
}

// CloseLog returns a year's lock/unlock audit trail.
func (h *Handler) CloseLog(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	yearID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || yearID <= 0 {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	store, ok := h.deps.Store.(CloseLogStore)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "close log not configured")
		return
	}
	list, err := store.ListCloseLog(r.Context(), h.deps.DB, entityID, yearID)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// ExportFEC downloads posted entries in the range as a FEC file.
func (h *Handler) ExportFEC(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	now := time.Now().UTC()
	entries, err := h.deps.Store.LedgerBook(r.Context(), h.deps.DB, entityID,
		dateParam(r, "from", time.Time{}), dateParam(r, "to", now))
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	accts, err := h.deps.Store.Accounts(r.Context(), h.deps.DB, entityID)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	journals, err := h.deps.Store.ListJournals(r.Context(), h.deps.DB, entityID)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	am := make(map[int64]Account, len(accts))
	for _, a := range accts {
		am[a.ID] = a
	}
	jm := make(map[int64]Journal, len(journals))
	for _, j := range journals {
		jm[j.ID] = j
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=fec.txt")
	if err := WriteFEC(w, BuildFECRows(entries, jm, am)); err != nil {
		platform.WriteError(w, err)
		return
	}
}
