package sales

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Deps wires handlers to persistence, catalog (for fulfillment), and events.
type Deps struct {
	Store   Store
	Catalog catalog.Store // nil disables fulfillment validation of stock effects
	Bus     platform.Bus
}

// Middleware builds Require-style RBAC gates.
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the sales surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("sales", "document", "write")).Post("/sales/documents", h.CreateDoc)
	r.With(mw("sales", "document", "read")).Get("/sales/documents", h.ListDocs)
	r.With(mw("sales", "document", "read")).Get("/sales/documents/{id}", h.GetDoc)
	r.With(mw("sales", "document", "write")).Put("/sales/documents/{id}", h.UpdateDoc)
	r.With(mw("sales", "document", "validate")).Post("/sales/documents/{id}/status", h.SetStatus)
	r.With(mw("sales", "document", "write")).Post("/sales/documents/{id}/convert", h.ConvertDoc)
	r.With(mw("sales", "payment", "write")).Post("/sales/payments", h.RecordPayment)
	r.With(mw("sales", "shipment", "write")).Post("/sales/shipments/{id}/fulfill", h.Fulfill)
	r.With(mw("sales", "credit", "write")).Post("/sales/credit-notes", h.CreateCreditNote)
	r.With(mw("sales", "credit", "write")).Post("/sales/credit-notes/{id}/apply", h.ApplyCredit)
}

// Handler implements the sales HTTP surface.
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

func yearMonth() string { return time.Now().UTC().Format("200601") }

// CreateDoc creates a draft document (refs assigned server-side; totals computed).
func (h *Handler) CreateDoc(w http.ResponseWriter, r *http.Request) {
	var d Document
	if err := decode(r, &d); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	d.ID, d.Ref, d.Status, d.EntityID = 0, "", 0, entityOf(r)
	if d.Currency == "" {
		d.Currency = "USD"
	}
	if d.RateToBase == 0 {
		d.RateToBase = 1000000
	}
	if err := h.deps.Store.CreateDoc(r.Context(), &d, yearMonth()); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

// ListDocs pages documents of one family.
func (h *Handler) ListDocs(w http.ResponseWriter, r *http.Request) {
	t := documents.DocType(r.URL.Query().Get("type"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	list, err := h.deps.Store.ListDocs(r.Context(), entityOf(r), t, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// GetDoc fetches one document.
func (h *Handler) GetDoc(w http.ResponseWriter, r *http.Request) {
	d, ok := h.load(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// UpdateDoc replaces a DRAFT document's lines (validated docs immutable — 422).
func (h *Handler) UpdateDoc(w http.ResponseWriter, r *http.Request) {
	d, ok := h.load(w, r)
	if !ok {
		return
	}
	if d.Status != 0 {
		writeErr(w, http.StatusUnprocessableEntity, "only draft documents can be edited")
		return
	}
	var body struct {
		Lines []documents.Line `json:"lines"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	upd, err := h.deps.Store.UpdateDocLines(r.Context(), d.ID, body.Lines)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, upd)
}

func (h *Handler) load(w http.ResponseWriter, r *http.Request) (Document, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return Document{}, false
	}
	d, err := h.deps.Store.DocByID(r.Context(), id)
	if err != nil || d.EntityID != entityOf(r) {
		writeErr(w, http.StatusNotFound, "document not found")
		return Document{}, false
	}
	return d, true
}

// SetStatus applies a kernel-checked transition.
func (h *Handler) SetStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var req struct {
		To int16 `json:"to"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	d, err := h.deps.Store.SetStatus(r.Context(), id, req.To)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	if h.deps.Bus != nil {
		_ = h.deps.Bus.Publish(r.Context(), platform.Event{
			Subject: "forgeerp.sales.document.status.v1", Entity: string(d.Type), ID: d.ID})
	}
	writeJSON(w, http.StatusOK, d)
}

// ConvertDoc clones a document into the next family with lineage.
func (h *Handler) ConvertDoc(w http.ResponseWriter, r *http.Request) {
	src, ok := h.load(w, r)
	if !ok {
		return
	}
	var req struct {
		To documents.DocType `json:"to"`
	}
	if err := decode(r, &req); err != nil || req.To == "" {
		writeErr(w, http.StatusBadRequest, "target type required")
		return
	}
	next, err := Convert(src, req.To)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	next.EntityID = entityOf(r)
	out := &next
	if err := h.deps.Store.CreateDoc(r.Context(), out, yearMonth()); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

type paymentRequest struct {
	OrgID      int64   `json:"org_id"`
	Amount     int64   `json:"amount"`
	Currency   string  `json:"currency"`
	Method     string  `json:"method"`
	InvoiceIDs []int64 `json:"invoice_ids"`
}

// RecordPayment records a payment and allocates it across invoices.
func (h *Handler) RecordPayment(w http.ResponseWriter, r *http.Request) {
	var req paymentRequest
	if err := decode(r, &req); err != nil || req.Amount <= 0 || len(req.InvoiceIDs) == 0 {
		writeErr(w, http.StatusBadRequest, "amount and invoice_ids required")
		return
	}
	if req.Currency == "" {
		req.Currency = "USD"
	}
	if req.Method == "" {
		req.Method = "transfer"
	}
	p := &Payment{EntityID: entityOf(r), OrgID: req.OrgID, Amount: req.Amount,
		Currency: req.Currency, Method: req.Method, PaidAt: time.Now().UTC()}
	applied, err := h.deps.Store.RecordPayment(r.Context(), p, req.InvoiceIDs, yearMonth())
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"payment": p, "applied": applied})
}

type fulfillLine struct {
	ProductID   int64 `json:"product_id"`
	WarehouseID int64 `json:"warehouse_id"`
	Qty         int64 `json:"qty"`
}

// Fulfill posts stock shipments for a validated shipment (Phase 05 integration:
// each line becomes a catalog shipment movement; oversell → 422).
func (h *Handler) Fulfill(w http.ResponseWriter, r *http.Request) {
	d, ok := h.load(w, r)
	if !ok {
		return
	}
	if d.Type != documents.TypeShipment {
		writeErr(w, http.StatusUnprocessableEntity, "fulfillment applies to shipments")
		return
	}
	if d.Status != ShipmentValidated {
		writeErr(w, http.StatusUnprocessableEntity, "shipment must be validated first")
		return
	}
	if h.deps.Catalog == nil {
		writeErr(w, http.StatusNotImplemented, "catalog store not wired")
		return
	}
	var req struct {
		Lines []fulfillLine `json:"lines"`
	}
	if err := decode(r, &req); err != nil || len(req.Lines) == 0 {
		writeErr(w, http.StatusBadRequest, "fulfillment lines required")
		return
	}
	for _, l := range req.Lines {
		m := catalog.StockMovement{EntityID: entityOf(r), ProductID: l.ProductID,
			WarehouseID: l.WarehouseID, Qty: -l.Qty, Reason: catalog.ReasonShipment, Ref: d.Ref}
		if _, err := h.deps.Catalog.AppendMovement(r.Context(), &m, false); err != nil {
			writeErr(w, storeErrorCode(err), err.Error())
			return
		}
	}
	closed, err := h.deps.Store.SetStatus(r.Context(), d.ID, ShipmentClosed)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, closed)
}

func storeErrorCode(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case strings.Contains(err.Error(), "not found"):
		return http.StatusNotFound
	case strings.Contains(err.Error(), "conflict"):
		return http.StatusConflict
	case strings.Contains(err.Error(), "overpayment"):
		return http.StatusUnprocessableEntity
	case strings.Contains(err.Error(), "illegal transition"):
		return http.StatusUnprocessableEntity
	default:
		return http.StatusUnprocessableEntity
	}
}

// CreateCreditNote clones an invoice's lines into a draft credit note.
func (h *Handler) CreateCreditNote(w http.ResponseWriter, r *http.Request) {
	var req struct {
		InvoiceID int64 `json:"invoice_id"`
	}
	if err := decode(r, &req); err != nil || req.InvoiceID <= 0 {
		writeErr(w, http.StatusBadRequest, "invoice_id required")
		return
	}
	src, err := h.deps.Store.DocByID(r.Context(), req.InvoiceID)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	if src.Type != documents.TypeInvoice {
		writeErr(w, http.StatusUnprocessableEntity, "sales: source must be an invoice")
		return
	}
	credit := &Document{EntityID: src.EntityID, Type: documents.TypeCreditNote,
		OrgID: src.OrgID, Currency: src.Currency, RateToBase: src.RateToBase,
		SourceType: documents.TypeInvoice, SourceID: src.ID, Lines: src.Lines}
	if err := h.deps.Store.CreateDoc(r.Context(), credit, yearMonth()); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, credit)
}

// ApplyCredit allocates a validated credit note against an invoice balance.
func (h *Handler) ApplyCredit(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var req struct {
		InvoiceID int64 `json:"invoice_id"`
		Amount    int64 `json:"amount"`
	}
	if err := decode(r, &req); err != nil || req.InvoiceID <= 0 {
		writeErr(w, http.StatusBadRequest, "invoice_id and amount required")
		return
	}
	if err := h.deps.Store.ApplyCredit(r.Context(), req.InvoiceID, id, req.Amount); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	bal, err := h.deps.Store.InvoiceBalance(r.Context(), req.InvoiceID)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"invoice_id": req.InvoiceID, "balance": bal})
}
