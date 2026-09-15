package finance

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

// Deps wires handlers to persistence.
type Deps struct {
	Store Store
	DB    platform.DBTX
}

// Middleware builds Require-style RBAC gates.
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the finance surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("finance", "account", "write")).Post("/finance/accounts", h.CreateAccount)
	r.With(mw("finance", "account", "read")).Get("/finance/accounts", h.ListAccounts)
	r.With(mw("finance", "journal", "write")).Post("/finance/journals", h.CreateJournal)
	r.With(mw("finance", "journal", "read")).Get("/finance/journals", h.ListJournals)
	r.With(mw("finance", "entry", "write")).Post("/finance/entries", h.PostEntry)
	r.With(mw("finance", "entry", "read")).Get("/finance/trial-balance", h.TrialBalance)
	r.With(mw("finance", "entry", "read")).Get("/finance/chain-verify", h.VerifyChain)
	r.With(mw("finance", "bank", "write")).Post("/finance/bank-accounts", h.CreateBankAccount)
	r.With(mw("finance", "bank", "read")).Get("/finance/bank-accounts", h.ListBankAccounts)
	r.With(mw("finance", "bank", "write")).Post("/finance/bank-transactions", h.RecordTransaction)
	r.With(mw("finance", "bank", "write")).Post("/finance/bank-transactions/{id}/reconcile", h.Reconcile)
	r.With(mw("finance", "loan", "write")).Post("/finance/loans", h.CreateLoan)
	r.With(mw("finance", "loan", "read")).Get("/finance/loans", h.ListLoans)
	// Phase 2: statement import, transfers, ledger book, close, tax.
	r.With(mw("finance", "bank", "write")).Post("/finance/bank-imports", h.ImportStatement)
	r.With(mw("finance", "bank", "read")).Get("/finance/bank-accounts/{id}/statement", h.BankStatement)
	r.With(mw("finance", "bank", "write")).Post("/finance/bank-transfers", h.Transfer)
	r.With(mw("finance", "bank", "read")).Post("/finance/bank-accounts/{id}/reconcile-match", h.ReconcileMatch)
	r.With(mw("finance", "entry", "read")).Get("/finance/ledger-book", h.LedgerBook)
	r.With(mw("finance", "entry", "read")).Get("/finance/ledger-accounts/{id}", h.AccountLedger)
	r.With(mw("finance", "entry", "write")).Post("/finance/fiscal-years", h.CreateFiscalYear)
	r.With(mw("finance", "entry", "read")).Get("/finance/fiscal-years", h.ListFiscalYears)
	r.With(mw("finance", "entry", "write")).Post("/finance/fiscal-years/{id}/close", h.CloseYear)
	r.With(mw("finance", "tax", "write")).Post("/finance/tax-periods", h.CreateTaxPeriod)
	r.With(mw("finance", "tax", "read")).Get("/finance/tax-periods", h.ListTaxPeriods)
	r.With(mw("finance", "tax", "write")).Post("/finance/tax-periods/{id}/status", h.SetTaxPeriodStatus)
	r.With(mw("finance", "tax", "write")).Post("/finance/vat-rates", h.CreateVATRate)
	r.With(mw("finance", "tax", "read")).Get("/finance/vat-rates", h.ListVATRates)
	r.With(mw("finance", "tax", "read")).Get("/finance/vat-return", h.VATReturn)
	r.With(mw("finance", "tax", "write")).Post("/finance/charges", h.CreateCharge)
	r.With(mw("finance", "tax", "read")).Get("/finance/charges", h.ListCharges)
	r.With(mw("finance", "tax", "read")).Get("/finance/charges-due", h.ChargesDue)
	r.With(mw("finance", "tax", "write")).Post("/finance/charges/{id}/pay", h.PayCharge)
}

// Handler implements the finance HTTP surface.
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

// CreateAccount adds a chart-of-accounts row.
func (h *Handler) CreateAccount(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var a Account
	if err := decode(r, &a); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	a.ID, a.EntityID = 0, entityID
	if err := h.deps.Store.CreateAccount(r.Context(), h.deps.DB, &a); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

// CreateJournal adds a journal.
func (h *Handler) CreateJournal(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var j Journal
	if err := decode(r, &j); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	j.ID, j.EntityID = 0, entityID
	if err := h.deps.Store.CreateJournal(r.Context(), h.deps.DB, &j); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, j)
}

type postEntryRequest struct {
	JournalID int64       `json:"journal_id"`
	Ref       string      `json:"ref"`
	Date      time.Time   `json:"date"`
	Memo      string      `json:"memo"`
	Lines     []EntryLine `json:"lines"`
}

// PostEntry validates, chain-links, and posts an entry (422 when unbalanced/locked).
func (h *Handler) PostEntry(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var req postEntryRequest
	if err := decode(r, &req); err != nil || req.JournalID == 0 || req.Ref == "" {
		writeErr(w, http.StatusBadRequest, "journal_id and ref required")
		return
	}
	if req.Date.IsZero() {
		req.Date = time.Now().UTC()
	}
	u, _ := identity.AuthUser(r)
	e := &Entry{EntityID: entityID, JournalID: req.JournalID, Ref: req.Ref,
		Date: req.Date, Memo: req.Memo, Lines: req.Lines, CreatedBy: &u.ID}
	if err := h.deps.Store.PostEntry(r.Context(), h.deps.DB, e); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, e)
}

// ListAccounts lists the chart of accounts within the caller's entity.
func (h *Handler) ListAccounts(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	list, err := h.deps.Store.Accounts(r.Context(), h.deps.DB, entityID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// ListJournals lists journals.
func (h *Handler) ListJournals(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	list, err := h.deps.Store.ListJournals(r.Context(), h.deps.DB, entityID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// ListBankAccounts lists bank accounts.
func (h *Handler) ListBankAccounts(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	list, err := h.deps.Store.ListBankAccounts(r.Context(), h.deps.DB, entityID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// ListLoans lists loans.
func (h *Handler) ListLoans(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	list, err := h.deps.Store.ListLoans(r.Context(), h.deps.DB, entityID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// TrialBalance returns per-account sums; callers assert debits == credits.
func (h *Handler) TrialBalance(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	tb, err := h.deps.Store.TrialBalance(r.Context(), h.deps.DB, entityID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "trial failed")
		return
	}
	var dr, cr int64
	out := map[string]map[string]int64{}
	for id, sums := range tb {
		dr += sums[0]
		cr += sums[1]
		out[strconv.FormatInt(id, 10)] = map[string]int64{"debit": sums[0], "credit": sums[1]}
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": out, "total_debit": dr, "total_credit": cr, "balanced": dr == cr})
}

// VerifyChain recomputes the tamper-evident chain for a journal.
func (h *Handler) VerifyChain(w http.ResponseWriter, r *http.Request) {
	jid, err := strconv.ParseInt(r.URL.Query().Get("journal_id"), 10, 64)
	if err != nil || jid == 0 {
		writeErr(w, http.StatusBadRequest, "journal_id required")
		return
	}
	ents, err := h.deps.Store.EntriesByJournal(r.Context(), h.deps.DB, jid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "load failed")
		return
	}
	if err := VerifyChain(ents); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": err.Error(), "entries": len(ents)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": true, "entries": len(ents)})
}

// CreateBankAccount opens a bank account.
func (h *Handler) CreateBankAccount(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var a BankAccount
	if err := decode(r, &a); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	a.ID, a.EntityID = 0, entityID
	if err := h.deps.Store.CreateBankAccount(r.Context(), h.deps.DB, &a); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

// RecordTransaction records a bank movement.
func (h *Handler) RecordTransaction(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var t BankTransaction
	if err := decode(r, &t); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	t.ID, t.EntityID = 0, entityID
	if t.ValueDate.IsZero() {
		t.ValueDate = time.Now().UTC()
	}
	if err := h.deps.Store.RecordTransaction(r.Context(), h.deps.DB, &t); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

// Reconcile marks a transaction reconciled.
func (h *Handler) Reconcile(w http.ResponseWriter, r *http.Request) {
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
	if err := h.deps.Store.Reconcile(r.Context(), h.deps.DB, entityID, id, time.Now().UTC()); err != nil {
		platform.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type loanRequest struct {
	Label     string    `json:"label"`
	Principal int64     `json:"principal"`
	RateBps   int       `json:"rate_bps"`
	Start     time.Time `json:"start"`
	Periods   int       `json:"periods"`
}

// CreateLoan books a loan with a generated schedule.
func (h *Handler) CreateLoan(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var req loanRequest
	if err := decode(r, &req); err != nil || req.Principal <= 0 || req.Periods <= 0 {
		writeErr(w, http.StatusBadRequest, "label, principal and periods required")
		return
	}
	if req.Start.IsZero() {
		req.Start = time.Now().UTC()
	}
	l := &Loan{EntityID: entityID, Label: req.Label, Principal: req.Principal,
		RateBps: req.RateBps, Start: req.Start, Periods: req.Periods}
	if err := h.deps.Store.CreateLoan(r.Context(), h.deps.DB, l); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, l)
}
