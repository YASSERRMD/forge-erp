package reporting

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
)

// Deps wires report handlers to the ledger, billing and stock seams.
type Deps struct {
	Ledger  Ledger
	Billing Billing
	Stock   Stock
	Orgs    Orgs
}

// Middleware builds Require-style RBAC gates (identity.Handler.Require in production).
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the reporting surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("reporting", "pnl", "read")).Get("/reports/pnl", h.PNL)
	r.With(mw("reporting", "receivables", "read")).Get("/reports/receivables", h.Receivables)
	r.With(mw("reporting", "intraeu", "read")).Get("/reports/intra-eu", h.IntraEU)
	r.With(mw("reporting", "monthly", "read")).Get("/reports/sales-monthly", h.Monthly)
	r.With(mw("reporting", "margin", "read")).Get("/reports/margins", h.Margins)
	r.With(mw("reporting", "valuation", "read")).Get("/reports/stock-valuation", h.Valuation)
}

// Handler implements the reporting HTTP surface.
type Handler struct{ deps Deps }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func entityOf(r *http.Request) int64 {
	if u, ok := identity.AuthUser(r); ok && u.EntityID != 0 {
		return u.EntityID
	}
	return 1
}

// PNL serves profit & loss over all posted entries.
func (h *Handler) PNL(w http.ResponseWriter, r *http.Request) {
	pnl, err := BuildPNL(r.Context(), entityOf(r), h.deps.Ledger)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "report failed")
		return
	}
	writeJSON(w, http.StatusOK, pnl)
}

// Receivables serves validated invoices with outstanding balances.
func (h *Handler) Receivables(w http.ResponseWriter, r *http.Request) {
	rows, total, err := Receivables(r.Context(), entityOf(r), h.deps.Billing)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "report failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": rows, "total": total})
}

// IntraEU serves intra-EU dispatches by destination country (?home=FR).
func (h *Handler) IntraEU(w http.ResponseWriter, r *http.Request) {
	rows, err := IntraEU(r.Context(), entityOf(r), r.URL.Query().Get("home"), h.deps.Billing, h.deps.Orgs)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "report failed")
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// Monthly serves the 12-month invoice revenue series.
func (h *Handler) Monthly(w http.ResponseWriter, r *http.Request) {
	points, err := SalesMonthly(r.Context(), entityOf(r), h.deps.Billing)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "report failed")
		return
	}
	writeJSON(w, http.StatusOK, points)
}

// Margins serves per-product sales margins.
func (h *Handler) Margins(w http.ResponseWriter, r *http.Request) {
	rows, err := ProductMargins(r.Context(), entityOf(r), h.deps.Billing, h.deps.Stock)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "report failed")
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// Valuation serves PMP stock valuation for one warehouse.
func (h *Handler) Valuation(w http.ResponseWriter, r *http.Request) {
	wh, err := strconv.ParseInt(r.URL.Query().Get("warehouse_id"), 10, 64)
	if err != nil || wh <= 0 {
		writeErr(w, http.StatusBadRequest, "warehouse_id required")
		return
	}
	rows, total, err := StockValuation(r.Context(), entityOf(r), wh, h.deps.Stock)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "report failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": rows, "total": total})
}
