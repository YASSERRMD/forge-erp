package hr

import (
	"net/http"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

type payrollRunIn struct {
	Label       string    `json:"label"`
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
}

// CreatePayrollRun opens a draft payroll run (amounts live on lines as
// given/recorded inputs; calculation is out of scope).
func (h *Handler) CreatePayrollRun(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var in payrollRunIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	run := PayrollRun{EntityID: entityID, Label: in.Label,
		PeriodStart: in.PeriodStart, PeriodEnd: in.PeriodEnd, Status: PayrollRunDraft}
	if err := h.deps.Store.CreatePayrollRun(r.Context(), h.deps.DB, &run); err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.hr.payroll.created.v1", "payroll_run", run.ID)
	writeJSON(w, http.StatusCreated, run)
}

// ListPayrollRuns lists payroll runs.
func (h *Handler) ListPayrollRuns(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	list, err := h.deps.Store.PayrollRunsOf(r.Context(), h.deps.DB, entityID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type payrollLineIn struct {
	SalaryID *int64 `json:"salary_id"`
	Gross    int64  `json:"gross"`
	Charges  int64  `json:"charges"`
	Net      int64  `json:"net"`
}

// AddPayrollRunLine records one given salary cost on a draft run.
func (h *Handler) AddPayrollRunLine(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	rid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var in payrollLineIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	l := PayrollRunLine{EntityID: entityID, RunID: rid,
		SalaryID: in.SalaryID, Gross: in.Gross, Charges: in.Charges, Net: in.Net}
	if err := h.deps.Store.AddPayrollRunLine(r.Context(), h.deps.DB, &l); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, l)
}

// ListPayrollRunLines lists a run's recorded lines.
func (h *Handler) ListPayrollRunLines(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	rid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	list, err := h.deps.Store.PayrollRunLines(r.Context(), h.deps.DB, entityID, rid)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type postRunIn struct {
	JournalID      int64 `json:"journal_id"`
	ExpenseAccount int64 `json:"expense_account_id"`
	BankAccount    int64 `json:"bank_account_id"`
	PayableAccount int64 `json:"payable_account_id"`
	RowVersion     int64 `json:"row_version"`
}

// PostPayrollRun posts a draft run through the payroll service: status flip
// and balanced ledger entry (debit salary expense gross, credit charges
// payable + bank net) commit atomically.
func (h *Handler) PostPayrollRun(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	rid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if h.deps.Finance == nil {
		writeErr(w, http.StatusServiceUnavailable, "hr: finance posting not wired")
		return
	}
	var in postRunIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if in.JournalID <= 0 || in.ExpenseAccount <= 0 || in.BankAccount <= 0 {
		writeErr(w, http.StatusUnprocessableEntity, "hr: journal, expense and bank accounts required")
		return
	}
	posted, err := h.payroll.Post(r.Context(), PostRunCmd{
		EntityID: entityID, RunID: rid, JournalID: in.JournalID,
		ExpenseAccount: in.ExpenseAccount, BankAccount: in.BankAccount,
		PayableAccount: in.PayableAccount, RowVersion: in.RowVersion})
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, posted)
}
