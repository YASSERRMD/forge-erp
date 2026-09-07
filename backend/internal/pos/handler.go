package pos

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/sales"
)

// Catalog abstracts the product/ledger reads and postings used at checkout.
type Catalog interface {
	ProductByID(ctx context.Context, id int64) (catalog.Product, error)
	Level(ctx context.Context, productID, warehouseID int64) (catalog.StockLevel, error)
	AppendMovement(ctx context.Context, m *catalog.StockMovement, allowNegative bool) (catalog.StockLevel, error)
}

// Sales abstracts the invoice/payment postings used at checkout.
type Sales interface {
	CreateDoc(ctx context.Context, d *sales.Document, yearMonth string) error
	SetStatus(ctx context.Context, id int64, to int16) (sales.Document, error)
	RecordPayment(ctx context.Context, p *sales.Payment, invoiceIDs []int64, yearMonth string) ([]int64, error)
}

// Deps wires handlers to persistence, the catalog/sales seams, and the bus.
type Deps struct {
	Store     Store
	Catalog   Catalog
	Sales     Sales
	WalkinOrg int64 // FERP_POS_WALKIN_ORG: default customer for anonymous sales (0 = require org)
	Bus       platform.Bus
}

// Middleware builds Require-style RBAC gates (identity.Handler.Require in production).
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the pos surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("pos", "terminal", "write")).Post("/pos/terminals", h.CreateTerminal)
	r.With(mw("pos", "terminal", "read")).Get("/pos/terminals", h.ListTerminals)
	r.With(mw("pos", "session", "write")).Post("/pos/sessions", h.OpenSession)
	r.With(mw("pos", "session", "read")).Get("/pos/sessions/{id}", h.GetSession)
	r.With(mw("pos", "session", "validate")).Post("/pos/sessions/{id}/close", h.CloseSession)
	r.With(mw("pos", "sale", "write")).Post("/pos/checkout", h.Checkout)
	r.With(mw("pos", "sale", "read")).Get("/pos/sessions/{id}/sales", h.SalesOfSession)
	r.With(mw("pos", "sale", "validate")).Post("/pos/sales/{id}/void", h.VoidSale)
}

// Handler implements the pos HTTP surface.
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

// CreateTerminal registers a till.
func (h *Handler) CreateTerminal(w http.ResponseWriter, r *http.Request) {
	var t Terminal
	if err := decode(r, &t); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	t.ID = 0
	t.EntityID = entityOf(r)
	t.Status = TerminalActive
	if err := h.deps.Store.CreateTerminal(r.Context(), &t); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

// ListTerminals lists tills within the caller's entity.
func (h *Handler) ListTerminals(w http.ResponseWriter, r *http.Request) {
	list, err := h.deps.Store.ListTerminals(r.Context(), entityOf(r))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type openSessionIn struct {
	TerminalID   int64  `json:"terminal_id"`
	Cashier      string `json:"cashier"`
	OpeningFloat int64  `json:"opening_float"`
}

// OpenSession starts a cashier shift.
func (h *Handler) OpenSession(w http.ResponseWriter, r *http.Request) {
	var in openSessionIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	se := &Session{EntityID: entityOf(r), TerminalID: in.TerminalID,
		Cashier: in.Cashier, OpeningFloat: in.OpeningFloat}
	if err := h.deps.Store.OpenSession(r.Context(), se); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	h.publish(r.Context(), "forgeerp.pos.session.opened.v1", "session", se.ID)
	writeJSON(w, http.StatusCreated, se)
}

// GetSession fetches one session.
func (h *Handler) GetSession(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	se, err := h.deps.Store.SessionByID(r.Context(), id)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, se)
}

type closeSessionIn struct {
	RowVersion int64 `json:"row_version"`
}

// CloseSession ends a cashier shift.
func (h *Handler) CloseSession(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var in closeSessionIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	se, err := h.deps.Store.CloseSession(r.Context(), id, in.RowVersion)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, se)
}

type checkoutIn struct {
	SessionID int64      `json:"session_id"`
	OrgID     int64      `json:"org_id"`
	Lines     []SaleLine `json:"lines"`
	Method    string     `json:"method"`
	Tendered  int64      `json:"tendered"`
}

// Checkout rings a sale: validates stock, posts a validated invoice + full
// payment, decrements tracked goods, and records the till sale.
func (h *Handler) Checkout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in checkoutIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	se, err := h.deps.Store.SessionByID(ctx, in.SessionID)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	if se.Status != SessionOpen {
		writeErr(w, http.StatusUnprocessableEntity, "pos: session closed")
		return
	}
	term, err := h.deps.Store.TerminalByID(ctx, se.TerminalID)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	if term.Status != TerminalActive {
		writeErr(w, http.StatusUnprocessableEntity, "pos: terminal inactive")
		return
	}
	entity := entityOf(r)
	dlines := make([]documents.Line, 0, len(in.Lines))
	type need struct {
		productID int64
		qty       int64
	}
	var needs []need
	for _, l := range in.Lines {
		if err := l.Validate(); err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		p, err := h.deps.Catalog.ProductByID(ctx, l.ProductID)
		if err != nil {
			writeErr(w, storeErrorCode(err), err.Error())
			return
		}
		if p.Status != catalog.ProductActive {
			writeErr(w, http.StatusUnprocessableEntity, "pos: product not sellable")
			return
		}
		dlines = append(dlines, documents.Line{ProductID: p.ID, Label: p.Name,
			Qty: l.Qty, UnitNet: p.NetPrice, VATRateBps: int(p.VATRateBps)})
		if p.Type == catalog.ProductGoods && p.StockTracked {
			lvl, err := h.deps.Catalog.Level(ctx, p.ID, term.WarehouseID)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, "stock check failed")
				return
			}
			if lvl.Qty < l.Qty {
				writeErr(w, http.StatusUnprocessableEntity, "pos: insufficient stock")
				return
			}
			needs = append(needs, need{productID: p.ID, qty: l.Qty})
		}
	}
	tot, err := documents.Sum(dlines)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	method := in.Method
	if method == "" {
		method = PayCash
	}
	orgID := in.OrgID
	if orgID == 0 {
		if h.deps.WalkinOrg == 0 {
			writeErr(w, http.StatusUnprocessableEntity, "pos: customer org required (no anonymous sales in lite scope)")
			return
		}
		orgID = h.deps.WalkinOrg
	}
	sale := Sale{EntityID: entity, SessionID: se.ID, OrgID: orgID,
		Lines: in.Lines, Method: method, Tendered: in.Tendered}
	if err := sale.Validate(); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	if in.Tendered < tot.Gross {
		writeErr(w, http.StatusUnprocessableEntity, "pos: tendered below total")
		return
	}
	ym := time.Now().UTC().Format("200601")
	inv := &sales.Document{EntityID: entity, Type: documents.TypeInvoice, OrgID: orgID,
		Currency: "USD", RateToBase: 1000000, Lines: dlines}
	if err := h.deps.Sales.CreateDoc(ctx, inv, ym); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	validated, err := h.deps.Sales.SetStatus(ctx, inv.ID, sales.InvoiceValidated)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	inv = &validated
	pay := &sales.Payment{EntityID: entity, OrgID: orgID, Amount: tot.Gross,
		Currency: "USD", Method: method, PaidAt: time.Now().UTC()}
	if _, err := h.deps.Sales.RecordPayment(ctx, pay, []int64{inv.ID}, ym); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	for _, n := range needs {
		qty := n.qty
		if _, err := h.deps.Catalog.AppendMovement(ctx, &catalog.StockMovement{
			EntityID: entity, ProductID: n.productID, WarehouseID: term.WarehouseID,
			Qty: -qty, Reason: catalog.ReasonShipment, Ref: inv.Ref}, false); err != nil {
			writeErr(w, storeErrorCode(err), err.Error())
			return
		}
	}
	rec := &Sale{EntityID: entity, SessionID: se.ID, Ref: inv.Ref, OrgID: orgID,
		Lines: in.Lines, TotalGross: tot.Gross, Method: method, Tendered: in.Tendered,
		Change: in.Tendered - tot.Gross, Status: SaleCompleted, InvoiceID: inv.ID}
	if err := h.deps.Store.CreateSale(ctx, rec); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	h.publish(ctx, "forgeerp.pos.sale.completed.v1", "sale", rec.ID)
	writeJSON(w, http.StatusCreated, rec)
}

// SalesOfSession lists a session's till sales.
func (h *Handler) SalesOfSession(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	list, err := h.deps.Store.SalesOfSession(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// VoidSale marks a completed sale void (underlying invoice/payment stand;
// reversals are explicit credit notes — see DIFFERENCES).
func (h *Handler) VoidSale(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	sa, err := h.deps.Store.VoidSale(r.Context(), id)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sa)
}
