package finance

import (
	"net/http"
	"strconv"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// svc builds the Phase 2 orchestration service: when Deps.DB is the live
// pool, transfers/closes/imports run inside platform.TxEntity; otherwise
// (memory tests) they run directly on the store.
func (h *Handler) svc() *Service {
	var pool *pgxpool.Pool
	if p, ok := h.deps.DB.(*pgxpool.Pool); ok {
		pool = p
	}
	return NewService(pool, h.deps.Store)
}

func dateParam(r *http.Request, name string, def time.Time) time.Time {
	s := r.URL.Query().Get(name)
	if s == "" {
		return def
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return def
}

type importRequest struct {
	AccountID int64  `json:"account_id"`
	Format    string `json:"format"` // csv | camt053
	Content   string `json:"content"`
}

// ImportStatement parses a CSV/CAMT.053 statement and imports it idempotently.
func (h *Handler) ImportStatement(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var req importRequest
	if err := decode(r, &req); err != nil || req.AccountID == 0 || req.Content == "" {
		writeErr(w, http.StatusBadRequest, "account_id, format and content required")
		return
	}
	imported, skipped, err := h.svc().ImportStatement(r.Context(), h.deps.DB, entityID, req.AccountID, req.Format, req.Content)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"imported": imported, "skipped": skipped})
}

// BankStatement returns movements with running balances.
func (h *Handler) BankStatement(w http.ResponseWriter, r *http.Request) {
	if _, entityErr := platform.EntityOf(r); entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	lines, err := h.deps.Store.BankStatement(r.Context(), h.deps.DB, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "statement failed")
		return
	}
	writeJSON(w, http.StatusOK, lines)
}

type transferRequest struct {
	FromAccountID int64     `json:"from_account_id"`
	ToAccountID   int64     `json:"to_account_id"`
	Amount        int64     `json:"amount"`
	Label         string    `json:"label"`
	Ref           string    `json:"ref"`
	ValueDate     time.Time `json:"value_date"`
}

// Transfer moves funds between two bank accounts as an atomic pair.
func (h *Handler) Transfer(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var req transferRequest
	if err := decode(r, &req); err != nil || req.Amount <= 0 {
		writeErr(w, http.StatusBadRequest, "from_account_id, to_account_id and positive amount required")
		return
	}
	cmd := TransferCmd{EntityID: entityID, FromAccountID: req.FromAccountID,
		ToAccountID: req.ToAccountID, Amount: req.Amount, Label: req.Label,
		Ref: req.Ref, ValueDate: req.ValueDate}
	if err := h.svc().Transfer(r.Context(), h.deps.DB, cmd); err != nil {
		platform.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type matchRequest struct {
	Lines      []ImportLine `json:"lines"`
	WindowDays int          `json:"window_days"`
}

// ReconcileMatch pairs candidate import lines to stored transactions.
func (h *Handler) ReconcileMatch(w http.ResponseWriter, r *http.Request) {
	if _, entityErr := platform.EntityOf(r); entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var req matchRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	txs, err := h.deps.Store.BankTransactions(r.Context(), h.deps.DB, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "load failed")
		return
	}
	matches := MatchTransactions(txs, req.Lines, time.Duration(req.WindowDays)*24*time.Hour)
	writeJSON(w, http.StatusOK, matches)
}

// LedgerBook returns entries with lines in date order.
func (h *Handler) LedgerBook(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	now := time.Now().UTC()
	ents, err := h.deps.Store.LedgerBook(r.Context(), h.deps.DB, entityID,
		dateParam(r, "from", time.Time{}), dateParam(r, "to", now))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "book failed")
		return
	}
	writeJSON(w, http.StatusOK, ents)
}

// AccountLedger drills one account's balance walk.
func (h *Handler) AccountLedger(w http.ResponseWriter, r *http.Request) {
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
	now := time.Now().UTC()
	drill, err := h.deps.Store.AccountLedger(r.Context(), h.deps.DB, entityID, id,
		dateParam(r, "from", time.Time{}), dateParam(r, "to", now))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "ledger failed")
		return
	}
	writeJSON(w, http.StatusOK, drill)
}

type fiscalYearRequest struct {
	Label string    `json:"label"`
	Start time.Time `json:"start_date"`
	End   time.Time `json:"end_date"`
}

// CreateFiscalYear opens a fiscal year.
func (h *Handler) CreateFiscalYear(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var req fiscalYearRequest
	if err := decode(r, &req); err != nil || req.Label == "" {
		writeErr(w, http.StatusBadRequest, "label, start_date and end_date required")
		return
	}
	f := &FiscalYear{EntityID: entityID, Label: req.Label, StartDate: req.Start, EndDate: req.End}
	if err := h.deps.Store.CreateFiscalYear(r.Context(), h.deps.DB, f); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, f)
}

// ListFiscalYears lists fiscal years.
func (h *Handler) ListFiscalYears(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	list, err := h.deps.Store.ListFiscalYears(r.Context(), h.deps.DB, entityID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type closeYearRequest struct {
	JournalID         int64     `json:"journal_id"`
	RetainedAccountID int64     `json:"retained_account_id"`
	Ref               string    `json:"ref"`
	Date              time.Time `json:"date"`
}

// CloseYear posts the P&L carry-forward and locks the fiscal year.
func (h *Handler) CloseYear(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	yearID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var req closeYearRequest
	if err := decode(r, &req); err != nil || req.JournalID == 0 || req.RetainedAccountID == 0 {
		writeErr(w, http.StatusBadRequest, "journal_id and retained_account_id required")
		return
	}
	e, err := h.svc().CloseYear(r.Context(), h.deps.DB, CloseCmd{EntityID: entityID,
		YearID: yearID, JournalID: req.JournalID, RetainedAccountID: req.RetainedAccountID,
		Ref: req.Ref, Date: req.Date})
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, e)
}

type taxPeriodRequest struct {
	Label string    `json:"label"`
	Start time.Time `json:"start_date"`
	End   time.Time `json:"end_date"`
}

// CreateTaxPeriod opens a declaration window.
func (h *Handler) CreateTaxPeriod(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var req taxPeriodRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	p := &TaxPeriod{EntityID: entityID, Label: req.Label, Start: req.Start, End: req.End}
	if err := h.deps.Store.CreateTaxPeriod(r.Context(), h.deps.DB, p); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

// ListTaxPeriods lists declaration windows.
func (h *Handler) ListTaxPeriods(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	list, err := h.deps.Store.ListTaxPeriods(r.Context(), h.deps.DB, entityID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// SetTaxPeriodStatus advances a period (open → filed → paid).
func (h *Handler) SetTaxPeriodStatus(w http.ResponseWriter, r *http.Request) {
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
	var req struct {
		Status TaxPeriodStatus `json:"status"`
	}
	if err := decode(r, &req); err != nil || req.Status == "" {
		writeErr(w, http.StatusBadRequest, "status required")
		return
	}
	if err := h.deps.Store.SetTaxPeriodStatus(r.Context(), h.deps.DB, entityID, id, req.Status); err != nil {
		platform.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// CreateVATRate registers VAT rate metadata.
func (h *Handler) CreateVATRate(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var v VATRate
	if err := decode(r, &v); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	v.ID, v.EntityID = 0, entityID
	if err := h.deps.Store.CreateVATRate(r.Context(), h.deps.DB, &v); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

// ListVATRates lists VAT rate metadata.
func (h *Handler) ListVATRates(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	list, err := h.deps.Store.ListVATRates(r.Context(), h.deps.DB, entityID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// VATReturn computes the VAT position over [from, to].
func (h *Handler) VATReturn(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	now := time.Now().UTC()
	ret, err := h.deps.Store.VATReturn(r.Context(), h.deps.DB, entityID,
		dateParam(r, "from", time.Time{}), dateParam(r, "to", now))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "vat failed")
		return
	}
	writeJSON(w, http.StatusOK, ret)
}

type chargeRequest struct {
	Label   string    `json:"label"`
	Kind    string    `json:"kind"`
	Amount  int64     `json:"amount"`
	DueDate time.Time `json:"due_date"`
}

// CreateCharge records a social/fiscal charge.
func (h *Handler) CreateCharge(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var req chargeRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	c := &TaxCharge{EntityID: entityID, Label: req.Label, Kind: req.Kind,
		Amount: req.Amount, DueDate: req.DueDate}
	if err := h.deps.Store.CreateCharge(r.Context(), h.deps.DB, c); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

// ListCharges lists charges in due order.
func (h *Handler) ListCharges(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	list, err := h.deps.Store.ListCharges(r.Context(), h.deps.DB, entityID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// ChargesDue lists unpaid charges due at or before as_of (default now).
func (h *Handler) ChargesDue(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	due, err := h.deps.Store.ChargesDue(r.Context(), h.deps.DB, entityID,
		dateParam(r, "as_of", time.Now().UTC()))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "due failed")
		return
	}
	writeJSON(w, http.StatusOK, due)
}

// PayCharge settles a charge.
func (h *Handler) PayCharge(w http.ResponseWriter, r *http.Request) {
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
	if err := h.deps.Store.MarkChargePaid(r.Context(), h.deps.DB, entityID, id, time.Now().UTC()); err != nil {
		platform.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
