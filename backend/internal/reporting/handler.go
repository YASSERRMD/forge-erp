package reporting

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

// Deps wires report handlers to the ledger, billing, purchasing and stock
// seams. Purchases is optional (nil skips the arrival side); main.go should
// wire the procurement store so arrivals are covered.
type Deps struct {
	Ledger    Ledger
	Billing   Billing
	Purchases Purchasing
	Stock     Stock
	Orgs      Orgs
	DB        platform.DBTX
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
	r.With(mw("reporting", "intraeu", "read")).Get("/reports/intra-eu.csv", h.IntraEUCSV)
	r.With(mw("reporting", "intraeu", "read")).Get("/reports/intra-eu.deb", h.IntraEUDEB)
	r.With(mw("reporting", "ledger", "read")).Get("/reports/ledger/account", h.LedgerAccount)
	r.With(mw("reporting", "ledger", "read")).Get("/reports/general-ledger.csv", h.GeneralLedgerCSV)
	r.With(mw("reporting", "close", "read")).Get("/reports/close-preview", h.ClosePreview)
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

// PNL serves profit & loss (?from=&to= YYYY-MM-DD bound the period;
// absent bounds report all posted entries).
func (h *Handler) PNL(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	q := r.URL.Query()
	from, to, err := parseDayBounds(q.Get("from"), q.Get("to"))
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	pnl, err := BuildPNLPeriod(r.Context(), h.deps.DB, entityID, h.deps.Ledger, from, to)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pnl)
}

// Receivables serves validated invoices with outstanding balances.
func (h *Handler) Receivables(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	rows, total, err := Receivables(r.Context(), h.deps.DB, entityID, h.deps.Billing)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "report failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": rows, "total": total})
}

// IntraEU serves intra-EU dispatches by destination country (?home=FR).
func (h *Handler) IntraEU(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	rows, err := IntraEU(r.Context(), h.deps.DB, entityID, r.URL.Query().Get("home"), h.deps.Billing, h.deps.Orgs)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "report failed")
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// Monthly serves the 12-month invoice revenue series.
func (h *Handler) Monthly(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	points, err := SalesMonthly(r.Context(), h.deps.DB, entityID, h.deps.Billing)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "report failed")
		return
	}
	writeJSON(w, http.StatusOK, points)
}

// Margins serves per-product sales margins.
func (h *Handler) Margins(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	rows, err := ProductMargins(r.Context(), h.deps.DB, entityID, h.deps.Billing, h.deps.Stock)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "report failed")
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// Valuation serves PMP stock valuation for one warehouse.
func (h *Handler) Valuation(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	wh, err := strconv.ParseInt(r.URL.Query().Get("warehouse_id"), 10, 64)
	if err != nil || wh <= 0 {
		writeErr(w, http.StatusBadRequest, "warehouse_id required")
		return
	}
	rows, total, err := StockValuation(r.Context(), h.deps.DB, entityID, wh, h.deps.Stock)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "report failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": rows, "total": total})
}

// parseDayBounds parses optional YYYY-MM-DD bounds (both or neither;
// inclusive day-wide UTC range). Bad input is ErrValidation (→ 422).
func parseDayBounds(fromS, toS string) (time.Time, time.Time, error) {
	if fromS == "" && toS == "" {
		return time.Time{}, time.Time{}, nil
	}
	if fromS == "" || toS == "" {
		return time.Time{}, time.Time{}, fmt.Errorf("reporting: from and to are required together: %w", platform.ErrValidation)
	}
	from, err := time.Parse("2006-01-02", fromS)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("reporting: bad from %q (want YYYY-MM-DD): %w", fromS, platform.ErrValidation)
	}
	to, err := time.Parse("2006-01-02", toS)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("reporting: bad to %q (want YYYY-MM-DD): %w", toS, platform.ErrValidation)
	}
	to = to.Add(24*time.Hour - time.Nanosecond)
	if from.After(to) {
		return time.Time{}, time.Time{}, fmt.Errorf("reporting: from after to: %w", platform.ErrValidation)
	}
	return from.UTC(), to.UTC(), nil
}

// intraMoves collects + filters movements for the file endpoints
// (?home=FR&period=YYYY-MM&direction=&flow=).
func (h *Handler) intraMoves(r *http.Request, entityID int64) ([]IntraMovement, string, error) {
	q := r.URL.Query()
	home := q.Get("home")
	moves, err := CollectIntraMovements(r.Context(), h.deps.DB, entityID, home,
		q.Get("period"), h.deps.Billing, h.deps.Purchases, h.deps.Orgs, h.deps.Stock)
	if err != nil {
		return nil, "", err
	}
	moves, err = FilterMovements(moves, q.Get("direction"), q.Get("flow"))
	if err != nil {
		return nil, "", err
	}
	return moves, home, nil
}

// IntraEUCSV serves the declaration movements as a downloadable CSV.
func (h *Handler) IntraEUCSV(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	moves, _, err := h.intraMoves(r, entityID)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	name := "intra-eu.csv"
	if p := r.URL.Query().Get("period"); p != "" {
		name = "intra-eu-" + p + ".csv"
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(FormatIntraCSV(moves)))
}

// IntraEUDEB serves the French DEB/DES fixed-width declaration file
// (?declarant= own VAT for the header; goods regimes 21/25, services 26/27).
func (h *Handler) IntraEUDEB(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	q := r.URL.Query()
	moves, home, err := h.intraMoves(r, entityID)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	period := q.Get("period")
	name := "deb.txt"
	if period != "" {
		name = "deb-" + period + ".txt"
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(FormatDEB(period, home, q.Get("declarant"), moves)))
}

// LedgerAccount serves the balance drill-down for one chart account
// (?account_id=; 404 unknown, 422 bad id).
func (h *Handler) LedgerAccount(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	id, err := strconv.ParseInt(r.URL.Query().Get("account_id"), 10, 64)
	if err != nil || id <= 0 {
		platform.WriteError(w, fmt.Errorf("reporting: account_id required: %w", platform.ErrValidation))
		return
	}
	drill, err := AccountDrillDown(r.Context(), h.deps.DB, entityID, id, h.deps.Ledger)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, drill)
}

// GeneralLedgerCSV serves every posted leg as a downloadable general-ledger CSV.
func (h *Handler) GeneralLedgerCSV(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	rows, err := GeneralLedger(r.Context(), h.deps.DB, entityID, h.deps.Ledger)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="general-ledger.csv"`)
	w.WriteHeader(http.StatusOK)
	_ = WriteGeneralLedgerCSV(w, rows)
}

// ClosePreview serves the computed closing proposal (never posts).
func (h *Handler) ClosePreview(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	prev, err := PreviewClose(r.Context(), h.deps.DB, entityID, h.deps.Ledger)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, prev)
}
