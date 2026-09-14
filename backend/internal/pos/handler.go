package pos

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/sales"
)

// Catalog abstracts the product/ledger reads and postings used at checkout.
type Catalog interface {
	ProductByID(ctx context.Context, db platform.DBTX, entityID, id int64) (catalog.Product, error)
	Level(ctx context.Context, db platform.DBTX, productID, warehouseID int64) (catalog.StockLevel, error)
	AppendMovement(ctx context.Context, db platform.DBTX, m *catalog.StockMovement, allowNegative bool) (catalog.StockLevel, error)
}

// Sales abstracts the invoice/payment postings used at checkout.
type Sales interface {
	CreateDoc(ctx context.Context, db platform.DBTX, d *sales.Document, yearMonth string) error
	DocByID(ctx context.Context, db platform.DBTX, entityID, id int64) (sales.Document, error)
	ListDocs(ctx context.Context, db platform.DBTX, entityID int64, t documents.DocType, limit, offset int) ([]sales.Document, error)
	SetStatus(ctx context.Context, db platform.DBTX, entityID, id int64, to int16) (sales.Document, error)
	RecordPayment(ctx context.Context, db platform.DBTX, p *sales.Payment, invoiceIDs []int64, yearMonth string) ([]int64, error)
	ApplyCredit(ctx context.Context, db platform.DBTX, entityID, invoiceID, creditID, amount int64) error
	InvoiceBalance(ctx context.Context, db platform.DBTX, entityID, invoiceID int64) (int64, error)
}

// Deps wires handlers to persistence, the catalog/sales seams, and the bus.
type Deps struct {
	Store     Store
	Catalog   Catalog
	Sales     Sales
	WalkinOrg int64 // FERP_POS_WALKIN_ORG: default customer for anonymous sales (0 = require org)
	Bus       platform.Bus
	DB        platform.DBTX
	Pool      *pgxpool.Pool // transaction source for the checkout service (nil in tests)
}

// Middleware builds Require-style RBAC gates (identity.Handler.Require in production).
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the pos surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d, svc: NewService(d.Pool, d.Store, d.Catalog, d.Sales, d.WalkinOrg, d.Bus)}
	r.With(mw("pos", "terminal", "write")).Post("/pos/terminals", h.CreateTerminal)
	r.With(mw("pos", "terminal", "read")).Get("/pos/terminals", h.ListTerminals)
	r.With(mw("pos", "session", "write")).Post("/pos/sessions", h.OpenSession)
	r.With(mw("pos", "session", "read")).Get("/pos/sessions/{id}", h.GetSession)
	r.With(mw("pos", "session", "validate")).Post("/pos/sessions/{id}/close", h.CloseSession)
	r.With(mw("pos", "sale", "write")).Post("/pos/checkout", h.Checkout)
	r.With(mw("pos", "sale", "read")).Get("/pos/sessions/{id}/sales", h.SalesOfSession)
	r.With(mw("pos", "sale", "validate")).Post("/pos/sales/{id}/void", h.VoidSale)
	r.With(mw("pos", "sale", "read")).Get("/pos/sales/{id}", h.GetSale)
	r.With(mw("pos", "sale", "validate")).Post("/pos/returns", h.ReturnSale)
}

// Handler implements the pos HTTP surface.
type Handler struct {
	deps Deps
	svc  *Service
}

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

// CreateTerminal registers a till.
func (h *Handler) CreateTerminal(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var t Terminal
	if err := decode(r, &t); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	t.ID = 0
	t.EntityID = entityID
	t.Status = TerminalActive
	if err := h.deps.Store.CreateTerminal(r.Context(), h.deps.DB, &t); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

// ListTerminals lists tills within the caller's entity.
func (h *Handler) ListTerminals(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	list, err := h.deps.Store.ListTerminals(r.Context(), h.deps.DB, entityID)
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
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var in openSessionIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	se := &Session{EntityID: entityID, TerminalID: in.TerminalID,
		Cashier: in.Cashier, OpeningFloat: in.OpeningFloat}
	if err := h.deps.Store.OpenSession(r.Context(), h.deps.DB, se); err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.pos.session.opened.v1", "session", se.ID)
	writeJSON(w, http.StatusCreated, se)
}

// GetSession fetches one session.
func (h *Handler) GetSession(w http.ResponseWriter, r *http.Request) {
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
	se, err := h.deps.Store.SessionByID(r.Context(), h.deps.DB, entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, se)
}

type closeSessionIn struct {
	RowVersion int64 `json:"row_version"`
}

// CloseSession ends a cashier shift.
func (h *Handler) CloseSession(w http.ResponseWriter, r *http.Request) {
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
	var in closeSessionIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	se, err := h.deps.Store.CloseSession(r.Context(), h.deps.DB, entityID, id, in.RowVersion)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, se)
}

type checkoutIn struct {
	SessionID int64    `json:"session_id"`
	OrgID     int64    `json:"org_id"`
	Lines     []SaleLine `json:"lines"`
	Method    string   `json:"method"`
	Tendered  int64    `json:"tendered"`
	Payments  []Tender `json:"payments"` // optional multi-tender legs
}

// Checkout rings a sale through the checkout service: validated invoice +
// full payment, tracked-goods decrements, and the till sale commit atomically.
func (h *Handler) Checkout(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var in checkoutIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	rec, err := h.svc.Checkout(r.Context(), CheckoutCmd{
		EntityID: entityID, SessionID: in.SessionID, OrgID: in.OrgID,
		Lines: in.Lines, Method: in.Method, Tendered: in.Tendered, Payments: in.Payments})
	if err != nil {
		// Checkout orchestration failures are business-rule rejections (the
		// service layer predates the kernel and returns plain errors).
		// Sentinel-wrapped store errors underneath still resolve to their
		// specific codes first via ErrorCode ordering (404/409 before 422).
		platform.WriteError(w, fmt.Errorf("%w: %w", err, platform.ErrValidation))
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

// SalesOfSession lists a session's till sales.
func (h *Handler) SalesOfSession(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	list, err := h.deps.Store.SalesOfSession(r.Context(), h.deps.DB, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// VoidSale marks a completed sale void (underlying invoice/payment stand;
// reversals are explicit credit notes — see DIFFERENCES).
func (h *Handler) VoidSale(w http.ResponseWriter, r *http.Request) {
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
	sa, err := h.deps.Store.VoidSale(r.Context(), h.deps.DB, entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sa)
}

// GetSale fetches one till sale with its lines.
func (h *Handler) GetSale(w http.ResponseWriter, r *http.Request) {
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
	sa, err := h.deps.Store.SaleByID(r.Context(), h.deps.DB, entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sa)
}

// ReturnSale reverses a completed till sale in full or in part: a validated
// credit note against the invoice (applied up to the open balance), inbound
// restock of tracked goods, and a returned marker once fully returned.
// Partial returns repeat until every line is covered; cash refunds for
// settled invoices happen out-of-band and are not modeled here.
func (h *Handler) ReturnSale(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	ctx := r.Context()
	var in struct {
		SaleID int64      `json:"sale_id"`
		Lines  []SaleLine `json:"lines"` // empty = everything remaining
	}
	if err := decode(r, &in); err != nil || in.SaleID <= 0 {
		writeErr(w, http.StatusBadRequest, "sale_id required")
		return
	}
	sa, err := h.deps.Store.SaleByID(ctx, h.deps.DB, entityID, in.SaleID)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	if sa.Status != SaleCompleted {
		writeErr(w, http.StatusUnprocessableEntity, "pos: only completed sales can be returned")
		return
	}
	se, err := h.deps.Store.SessionByID(ctx, h.deps.DB, entityID, sa.SessionID)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	term, err := h.deps.Store.TerminalByID(ctx, h.deps.DB, entityID, se.TerminalID)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	inv, err := h.deps.Sales.DocByID(ctx, h.deps.DB, sa.EntityID, sa.InvoiceID)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	// Remaining quantities = sold minus prior validated return credits.
	remaining := map[int64]int64{}
	priceOf := map[int64]documents.Line{}
	for _, l := range inv.Lines {
		remaining[l.ProductID] += l.Qty
		priceOf[l.ProductID] = l
	}
	credits, err := h.deps.Sales.ListDocs(ctx, h.deps.DB, sa.EntityID, documents.TypeCreditNote, 500, 0)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	for _, c := range credits {
		if c.SourceID != inv.ID || c.Status == 9 {
			continue
		}
		full, err := h.deps.Sales.DocByID(ctx, h.deps.DB, sa.EntityID, c.ID)
		if err != nil {
			continue
		}
		for _, l := range full.Lines {
			remaining[l.ProductID] -= l.Qty
		}
	}
	requested := in.Lines
	if len(requested) == 0 {
		for pid, qty := range remaining {
			if qty > 0 {
				requested = append(requested, SaleLine{ProductID: pid, Qty: qty})
			}
		}
	}
	if len(requested) == 0 {
		writeErr(w, http.StatusUnprocessableEntity, "pos: nothing left to return")
		return
	}
	var creditLines []documents.Line
	for _, q := range requested {
		if q.Qty <= 0 || q.Qty > remaining[q.ProductID] {
			writeErr(w, http.StatusUnprocessableEntity, "pos: return qty exceeds remaining")
			return
		}
		src := priceOf[q.ProductID]
		src.Qty = q.Qty
		creditLines = append(creditLines, src)
	}
	ym := time.Now().UTC().Format("200601")
	cn := &sales.Document{EntityID: sa.EntityID, Type: documents.TypeCreditNote,
		OrgID: inv.OrgID, Currency: inv.Currency, RateToBase: inv.RateToBase,
		SourceType: documents.TypeInvoice, SourceID: inv.ID, Lines: creditLines}
	if err := h.deps.Sales.CreateDoc(ctx, h.deps.DB, cn, ym); err != nil {
		platform.WriteError(w, err)
		return
	}
	validated, err := h.deps.Sales.SetStatus(ctx, h.deps.DB, sa.EntityID, cn.ID, 1)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	cn = &validated
	if bal, err := h.deps.Sales.InvoiceBalance(ctx, h.deps.DB, sa.EntityID, inv.ID); err == nil && bal > 0 {
		apply := cn.Totals.Gross
		if apply > bal {
			apply = bal
		}
		if err := h.deps.Sales.ApplyCredit(ctx, h.deps.DB, sa.EntityID, inv.ID, cn.ID, apply); err != nil {
			platform.WriteError(w, err)
			return
		}
	}
	for _, l := range creditLines {
		p, err := h.deps.Catalog.ProductByID(ctx, h.deps.DB, sa.EntityID, l.ProductID)
		if err != nil {
			continue // service/unknown lines simply have no stock effect
		}
		if p.Type != catalog.ProductGoods || !p.StockTracked {
			continue
		}
		if _, err := h.deps.Catalog.AppendMovement(ctx, h.deps.DB, &catalog.StockMovement{
			EntityID: sa.EntityID, ProductID: p.ID, WarehouseID: term.WarehouseID,
			Qty: l.Qty, Reason: catalog.ReasonReceipt, Ref: cn.Ref}, false); err != nil {
			platform.WriteError(w, err)
			return
		}
	}
	fully := true
	// Coverage including this credit.
	covered := map[int64]int64{}
	for _, l := range creditLines {
		covered[l.ProductID] += l.Qty
	}
	for pid, qty := range remaining {
		if covered[pid] < qty {
			fully = false
			break
		}
	}
	done := sa
	if fully {
		var err error
		done, err = h.deps.Store.MarkReturned(ctx, h.deps.DB, entityID, sa.ID)
		if err != nil {
			platform.WriteError(w, err)
			return
		}
	}
	h.publish(ctx, entityID, "forgeerp.pos.sale.returned.v1", "sale", done.ID)
	writeJSON(w, http.StatusOK, map[string]any{"sale": done, "credit_note": cn, "fully_returned": fully})
}
