package catalog

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Deps wires handlers to persistence, the event bus (nil-safe), and the stock policy.
type Deps struct {
	Store         Store
	Bus           platform.Bus
	AllowNegative bool // mirrors Dolibarr's negative-stock option
}

// Middleware builds Require-style RBAC gates (identity.Handler.Require in production).
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the catalog surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("catalog", "product", "write")).Post("/products", h.CreateProduct)
	r.With(mw("catalog", "product", "read")).Get("/products", h.ListProducts)
	r.With(mw("catalog", "product", "read")).Get("/products/{id}", h.GetProduct)
	r.With(mw("catalog", "warehouse", "write")).Post("/warehouses", h.CreateWarehouse)
	r.With(mw("catalog", "stock", "write")).Post("/stock-movements", h.AppendMovement)
	r.With(mw("catalog", "stock", "read")).Get("/stock-levels", h.GetLevel)
	r.With(mw("catalog", "stock", "write")).Post("/inventory-adjust", h.Adjust)
	r.With(mw("catalog", "variant", "write")).Post("/products/{id}/variants", h.CreateVariant)
	r.With(mw("catalog", "variant", "read")).Get("/products/{id}/variants", h.ListVariants)
}

// Handler implements the catalog HTTP surface.
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

// CreateProduct registers a product (409 on duplicate SKU).
func (h *Handler) CreateProduct(w http.ResponseWriter, r *http.Request) {
	var p Product
	if err := decode(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	p.ID = 0
	p.EntityID = entityOf(r)
	p.Status = ProductActive
	if err := p.Validate(); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := h.deps.Store.CreateProduct(r.Context(), &p); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

// ListProducts pages the catalog.
func (h *Handler) ListProducts(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	list, err := h.deps.Store.ListProducts(r.Context(), entityOf(r), limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// GetProduct fetches one product.
func (h *Handler) GetProduct(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	p, err := h.deps.Store.ProductByID(r.Context(), id)
	if err != nil || p.EntityID != entityOf(r) {
		writeErr(w, http.StatusNotFound, "product not found")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// CreateWarehouse opens a warehouse.
func (h *Handler) CreateWarehouse(w http.ResponseWriter, r *http.Request) {
	var wh Warehouse
	if err := decode(r, &wh); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	wh.ID = 0
	wh.EntityID = entityOf(r)
	wh.Status = 1
	if err := wh.Validate(); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := h.deps.Store.CreateWarehouse(r.Context(), &wh); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, wh)
}

type movementRequest struct {
	ProductID   int64          `json:"product_id"`
	WarehouseID int64          `json:"warehouse_id"`
	LotID       *int64         `json:"lot_id"`
	Qty         int64          `json:"qty"`
	UnitCost    int64          `json:"unit_cost"`
	Reason      MovementReason `json:"reason"`
	Ref         string         `json:"ref"`
}

// AppendMovement records a receipt/shipment/transfer (422 on guard violation).
func (h *Handler) AppendMovement(w http.ResponseWriter, r *http.Request) {
	var req movementRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	m := StockMovement{EntityID: entityOf(r), ProductID: req.ProductID, WarehouseID: req.WarehouseID,
		LotID: req.LotID, Qty: req.Qty, UnitCost: req.UnitCost, Reason: req.Reason, Ref: req.Ref}
	level, err := h.deps.Store.AppendMovement(r.Context(), &m, h.deps.AllowNegative)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	if h.deps.Bus != nil {
		_ = h.deps.Bus.Publish(r.Context(), platform.Event{
			Subject: "forgeerp.catalog.stock.moved.v1", Entity: "stock_movement", ID: m.ID})
	}
	writeJSON(w, http.StatusCreated, map[string]any{"movement": m, "level": level})
}

// GetLevel returns the on-hand position incl. PMP valuation.
func (h *Handler) GetLevel(w http.ResponseWriter, r *http.Request) {
	pid, _ := strconv.ParseInt(r.URL.Query().Get("product_id"), 10, 64)
	wid, _ := strconv.ParseInt(r.URL.Query().Get("warehouse_id"), 10, 64)
	if pid == 0 || wid == 0 {
		writeErr(w, http.StatusBadRequest, "product_id and warehouse_id required")
		return
	}
	level, err := h.deps.Store.Level(r.Context(), pid, wid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "level failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"level": level, "pmp": level.PMP()})
}

type adjustRequest struct {
	ProductID   int64  `json:"product_id"`
	WarehouseID int64  `json:"warehouse_id"`
	Qty         int64  `json:"qty"` // signed delta
	UnitCost    int64  `json:"unit_cost"`
	Ref         string `json:"ref"`
}

// Adjust posts an inventory-count correction (Dolibarr llx_inventory equivalent).
func (h *Handler) Adjust(w http.ResponseWriter, r *http.Request) {
	var req adjustRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	reason := ReasonAdjustIn
	if req.Qty < 0 {
		reason = ReasonAdjustOut
	}
	m := StockMovement{EntityID: entityOf(r), ProductID: req.ProductID, WarehouseID: req.WarehouseID,
		Qty: req.Qty, UnitCost: req.UnitCost, Reason: reason, Ref: req.Ref}
	level, err := h.deps.Store.AppendMovement(r.Context(), &m, h.deps.AllowNegative)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"movement": m, "level": level})
}

func storeErrorCode(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case strings.Contains(err.Error(), "duplicate"):
		return http.StatusConflict
	case strings.Contains(err.Error(), "not found"):
		return http.StatusNotFound
	case strings.Contains(err.Error(), "conflict"):
		return http.StatusConflict
	case strings.Contains(err.Error(), "insufficient stock"):
		return http.StatusUnprocessableEntity
	default:
		return http.StatusUnprocessableEntity
	}
}

// CreateVariant adds a sellable combination to a product.
func (h *Handler) CreateVariant(w http.ResponseWriter, r *http.Request) {
	pid, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || pid <= 0 {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var v Variant
	if err := decode(r, &v); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	v.ID = 0
	v.EntityID = entityOf(r)
	v.ProductID = pid
	if err := h.deps.Store.CreateVariant(r.Context(), &v); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

// ListVariants lists a product's combinations.
func (h *Handler) ListVariants(w http.ResponseWriter, r *http.Request) {
	pid, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || pid <= 0 {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	list, err := h.deps.Store.VariantsOf(r.Context(), pid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}
