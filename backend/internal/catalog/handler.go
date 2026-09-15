package catalog

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Deps wires handlers to persistence, the event bus (nil-safe), and the stock policy.
type Deps struct {
	Store         Store
	DB            platform.DBTX
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
	r.With(mw("catalog", "warehouse", "read")).Get("/warehouses", h.ListWarehouses)
	r.With(mw("catalog", "stock", "write")).Post("/stock-movements", h.AppendMovement)
	r.With(mw("catalog", "stock", "read")).Get("/stock-levels", h.GetLevel)
	r.With(mw("catalog", "stock", "write")).Post("/inventory-adjust", h.Adjust)
	r.With(mw("catalog", "variant", "write")).Post("/products/{id}/variants", h.CreateVariant)
	r.With(mw("catalog", "variant", "read")).Get("/products/{id}/variants", h.ListVariants)
	r.With(mw("catalog", "label", "write")).Post("/barcode/labels", h.LabelSheet)
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

// CreateProduct registers a product (409 on duplicate SKU).
func (h *Handler) CreateProduct(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var p Product
	if err := decode(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	p.ID = 0
	p.EntityID = entityID
	p.Status = ProductActive
	if err := p.Validate(); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := h.deps.Store.CreateProduct(r.Context(), h.deps.DB, &p); err != nil {
		platform.WriteError(w, err)
		return
	}
	if h.deps.Bus != nil {
		_ = h.deps.Bus.Publish(r.Context(), platform.Event{
			Subject: "forgeerp.catalog.product.created.v1", Entity: "product", EntityID: p.EntityID, ID: p.ID})
	}
	writeJSON(w, http.StatusCreated, p)
}

// ListProducts pages the catalog.
func (h *Handler) ListProducts(w http.ResponseWriter, r *http.Request) {
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
	list, err := h.deps.Store.ListProducts(r.Context(), h.deps.DB, entityID, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// GetProduct fetches one product.
func (h *Handler) GetProduct(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	p, err := h.deps.Store.ProductByID(r.Context(), h.deps.DB, entityID, id)
	if err != nil || p.EntityID != entityID {
		writeErr(w, http.StatusNotFound, "product not found")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// CreateWarehouse opens a warehouse.
func (h *Handler) CreateWarehouse(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var wh Warehouse
	if err := decode(r, &wh); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	wh.ID = 0
	wh.EntityID = entityID
	wh.Status = 1
	if err := wh.Validate(); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := h.deps.Store.CreateWarehouse(r.Context(), h.deps.DB, &wh); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, wh)
}

// ListWarehouses lists warehouses within the caller's entity.
func (h *Handler) ListWarehouses(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	list, err := h.deps.Store.ListWarehouses(r.Context(), h.deps.DB, entityID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
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
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var req movementRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	m := StockMovement{EntityID: entityID, ProductID: req.ProductID, WarehouseID: req.WarehouseID,
		LotID: req.LotID, Qty: req.Qty, UnitCost: req.UnitCost, Reason: req.Reason, Ref: req.Ref}
	level, err := h.deps.Store.AppendMovement(r.Context(), h.deps.DB, &m, h.deps.AllowNegative)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	if h.deps.Bus != nil {
		_ = h.deps.Bus.Publish(r.Context(), platform.Event{
			Subject: "forgeerp.catalog.stock.moved.v1", Entity: "stock_movement", EntityID: m.EntityID, ID: m.ID})
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
	level, err := h.deps.Store.Level(r.Context(), h.deps.DB, pid, wid)
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
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var req adjustRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	reason := ReasonAdjustIn
	if req.Qty < 0 {
		reason = ReasonAdjustOut
	}
	m := StockMovement{EntityID: entityID, ProductID: req.ProductID, WarehouseID: req.WarehouseID,
		Qty: req.Qty, UnitCost: req.UnitCost, Reason: reason, Ref: req.Ref}
	level, err := h.deps.Store.AppendMovement(r.Context(), h.deps.DB, &m, h.deps.AllowNegative)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"movement": m, "level": level})
}

// CreateVariant adds a sellable combination to a product.
func (h *Handler) CreateVariant(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
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
	v.EntityID = entityID
	v.ProductID = pid
	if err := h.deps.Store.CreateVariant(r.Context(), h.deps.DB, &v); err != nil {
		platform.WriteError(w, err)
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
	list, err := h.deps.Store.VariantsOf(r.Context(), h.deps.DB, pid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type labelSheetRequest struct {
	Items []LabelItem `json:"items"`
}

// LabelSheet validates label rows and returns the print-pipeline payload
// (label sheet JSON for the kernel-5 renderer; no image rendered here).
func (h *Handler) LabelSheet(w http.ResponseWriter, r *http.Request) {
	if _, entityErr := platform.EntityOf(r); entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var req labelSheetRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	sheet, err := BuildLabelSheet(req.Items)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, sheet)
}
