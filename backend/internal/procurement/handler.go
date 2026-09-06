package procurement

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

// Deps wires handlers to persistence, catalog (reception receipts), and events.
type Deps struct {
	Store   Store
	Catalog catalog.Store
	Bus     platform.Bus
}

// Middleware builds Require-style RBAC gates.
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the procurement surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("procurement", "document", "write")).Post("/purchase/documents", h.CreateDoc)
	r.With(mw("procurement", "document", "read")).Get("/purchase/documents", h.ListDocs)
	r.With(mw("procurement", "document", "read")).Get("/purchase/documents/{id}", h.GetDoc)
	r.With(mw("procurement", "document", "validate")).Post("/purchase/documents/{id}/status", h.SetStatus)
	r.With(mw("procurement", "document", "write")).Post("/purchase/documents/{id}/convert", h.ConvertDoc)
	r.With(mw("procurement", "document", "write")).Post("/purchase/documents/{id}/approve", h.Approve)
	r.With(mw("procurement", "price", "write")).Post("/purchase/prices", h.UpsertPrice)
	r.With(mw("procurement", "payment", "write")).Post("/purchase/payments", h.RecordPayment)
	r.With(mw("procurement", "reception", "write")).Post("/purchase/receptions/{id}/receive", h.Receive)
}

// Handler implements the procurement HTTP surface.
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

// CreateDoc creates a draft supplier document (contract prices enforced on order lines).
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
	if d.Type == documents.TypeSupplierOrder {
		for _, l := range d.Lines {
			if l.ProductID == 0 {
				continue
			}
			prices, err := h.deps.Store.PricesFor(r.Context(), d.EntityID, l.ProductID, d.OrgID)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, "price lookup failed")
				return
			}
			if err := CheckContract(l, d.OrgID, prices); err != nil {
				writeErr(w, http.StatusUnprocessableEntity, err.Error())
				return
			}
		}
	}
	if err := h.deps.Store.CreateDoc(r.Context(), &d, yearMonth()); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

// ListDocs pages supplier documents of one family.
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

// GetDoc fetches one supplier document.
func (h *Handler) GetDoc(w http.ResponseWriter, r *http.Request) {
	d, ok := h.load(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, d)
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

// SetStatus applies a kernel-checked transition (approval gate enforced in store).
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
	writeJSON(w, http.StatusOK, d)
}

// ConvertDoc clones into the next supplier family with lineage.
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

// Approve stamps the caller as approver so above-threshold orders can validate.
func (h *Handler) Approve(w http.ResponseWriter, r *http.Request) {
	d, ok := h.load(w, r)
	if !ok {
		return
	}
	u, _ := identity.AuthUser(r)
	updated, err := h.deps.Store.SetApproval(r.Context(), d.ID, u.ID)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// UpsertPrice pins a contract price.
func (h *Handler) UpsertPrice(w http.ResponseWriter, r *http.Request) {
	var p SupplierPrice
	if err := decode(r, &p); err != nil || p.ProductID == 0 || p.OrgID == 0 {
		writeErr(w, http.StatusBadRequest, "product_id and org_id required")
		return
	}
	p.ID, p.EntityID = 0, entityOf(r)
	if p.Currency == "" {
		p.Currency = "USD"
	}
	if err := h.deps.Store.UpsertPrice(r.Context(), &p); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

type paymentRequest struct {
	OrgID      int64   `json:"org_id"`
	Amount     int64   `json:"amount"`
	Currency   string  `json:"currency"`
	Method     string  `json:"method"`
	InvoiceIDs []int64 `json:"invoice_ids"`
}

// RecordPayment records a supplier payment and allocates it.
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
	p := &SupplierPayment{EntityID: entityOf(r), OrgID: req.OrgID, Amount: req.Amount,
		Currency: req.Currency, Method: req.Method}
	applied, err := h.deps.Store.RecordPayment(r.Context(), p, req.InvoiceIDs, yearMonth())
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"payment": p, "applied": applied})
}

type receiveLine struct {
	ProductID   int64 `json:"product_id"`
	WarehouseID int64 `json:"warehouse_id"`
	Qty         int64 `json:"qty"`
	UnitCost    int64 `json:"unit_cost"`
}

// Receive posts stock receipts for a validated reception (PMP via catalog).
func (h *Handler) Receive(w http.ResponseWriter, r *http.Request) {
	d, ok := h.load(w, r)
	if !ok {
		return
	}
	if d.Type != documents.TypeReception {
		writeErr(w, http.StatusUnprocessableEntity, "receiving applies to receptions")
		return
	}
	if d.Status != Validated {
		writeErr(w, http.StatusUnprocessableEntity, "reception must be validated first")
		return
	}
	if h.deps.Catalog == nil {
		writeErr(w, http.StatusNotImplemented, "catalog store not wired")
		return
	}
	var req struct {
		Lines []receiveLine `json:"lines"`
	}
	if err := decode(r, &req); err != nil || len(req.Lines) == 0 {
		writeErr(w, http.StatusBadRequest, "reception lines required")
		return
	}
	for _, l := range req.Lines {
		m := catalog.StockMovement{EntityID: entityOf(r), ProductID: l.ProductID,
			WarehouseID: l.WarehouseID, Qty: l.Qty, UnitCost: l.UnitCost,
			Reason: catalog.ReasonReceipt, Ref: d.Ref}
		if _, err := h.deps.Catalog.AppendMovement(r.Context(), &m, false); err != nil {
			writeErr(w, storeErrorCode(err), err.Error())
			return
		}
	}
	closed, err := h.deps.Store.SetStatus(r.Context(), d.ID, Stage2) // reception: validated → closed
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	if h.deps.Bus != nil {
		_ = h.deps.Bus.Publish(r.Context(), platform.Event{
			Subject: "forgeerp.procurement.reception.received.v1", Entity: "reception", ID: d.ID})
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
	case strings.Contains(err.Error(), "approval"):
		return http.StatusUnprocessableEntity
	default:
		return http.StatusUnprocessableEntity
	}
}
