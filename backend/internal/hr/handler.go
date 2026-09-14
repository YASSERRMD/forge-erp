package hr

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/YASSERRMD/forge-erp/backend/internal/finance"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Finance abstracts the ledger posting used when paying expenses.
type Finance interface {
	PostEntry(ctx context.Context, db platform.DBTX, e *finance.Entry) error
}

// Deps wires handlers to persistence and the event bus.
type Deps struct {
	Store   Store
	DB      platform.DBTX
	Finance Finance // nil disables expense payout posting
	Bus     platform.Bus
	Pool    *pgxpool.Pool // transaction source for the payout service (nil in tests)
}

// Middleware builds Require-style RBAC gates (identity.Handler.Require in production).
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the hr surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d, svc: NewService(d.Pool, d.Store, d.Finance, d.Bus)}
	r.With(mw("hr", "leave", "write")).Post("/hr/leaves", h.CreateLeave)
	r.With(mw("hr", "leave", "read")).Get("/hr/leaves", h.ListLeaves)
	r.With(mw("hr", "leave", "validate")).Post("/hr/leaves/{id}/status", h.SetLeaveStatus)
	r.With(mw("hr", "expense", "write")).Post("/hr/expenses", h.CreateExpense)
	r.With(mw("hr", "expense", "read")).Get("/hr/expenses", h.ListExpenses)
	r.With(mw("hr", "expense", "write")).Post("/hr/expenses/{id}/lines", h.AddExpenseLine)
	r.With(mw("hr", "expense", "validate")).Post("/hr/expenses/{id}/status", h.SetExpenseStatus)
	r.With(mw("hr", "expense", "pay")).Post("/hr/expenses/{id}/pay", h.PayExpense)
	r.With(mw("hr", "salary", "write")).Post("/hr/salaries", h.CreateSalary)
	r.With(mw("hr", "salary", "read")).Get("/hr/salaries", h.ListSalaries)
	r.With(mw("hr", "salary", "validate")).Post("/hr/salaries/{id}/status", h.SetSalaryStatus)
}

// Handler implements the hr HTTP surface.
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

func page(r *http.Request) (limit int, offset int, user string) {
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))
	user = r.URL.Query().Get("user")
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset, user
}

func (h *Handler) publish(ctx context.Context, entityID int64, subject, entity string, id int64) {
	if h.deps.Bus == nil {
		return
	}
	_ = h.deps.Bus.Publish(ctx, platform.Event{Subject: subject, Entity: entity, EntityID: entityID, ID: id})
}

type statusIn struct {
	Status     int16 `json:"status"`
	RowVersion int64 `json:"row_version"`
}

// CreateLeave files a draft leave request (days server-computed).
func (h *Handler) CreateLeave(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var l LeaveRequest
	if err := decode(r, &l); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	l.ID = 0
	l.EntityID = entityID
	l.Status = LeaveDraft
	if err := h.deps.Store.CreateLeave(r.Context(), h.deps.DB, &l); err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.hr.leave.created.v1", "leave", l.ID)
	writeJSON(w, http.StatusCreated, l)
}

// ListLeaves pages leave requests, optionally filtered by user.
func (h *Handler) ListLeaves(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	limit, offset, user := page(r)
	list, err := h.deps.Store.LeavesOf(r.Context(), h.deps.DB, entityID, user, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// SetLeaveStatus moves a leave request along its state machine.
func (h *Handler) SetLeaveStatus(w http.ResponseWriter, r *http.Request) {
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
	var in statusIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	l, err := h.deps.Store.SetLeaveStatus(r.Context(), h.deps.DB, entityID, id, LeaveStatus(in.Status), in.RowVersion)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

// CreateExpense opens a draft expense report.
func (h *Handler) CreateExpense(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var rep ExpenseReport
	if err := decode(r, &rep); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	rep.ID = 0
	rep.EntityID = entityID
	rep.Status = ExpenseDraft
	if err := h.deps.Store.CreateExpense(r.Context(), h.deps.DB, &rep); err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.hr.expense.created.v1", "expense", rep.ID)
	writeJSON(w, http.StatusCreated, rep)
}

// ListExpenses pages expense reports, optionally filtered by user.
func (h *Handler) ListExpenses(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	limit, offset, user := page(r)
	list, err := h.deps.Store.ExpensesOf(r.Context(), h.deps.DB, entityID, user, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// AddExpenseLine appends a line to a draft report (total recomputed).
func (h *Handler) AddExpenseLine(w http.ResponseWriter, r *http.Request) {
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
	var l ExpenseLine
	if err := decode(r, &l); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	l.ID = 0
	l.EntityID = entityID
	l.ReportID = rid
	if err := h.deps.Store.AddExpenseLine(r.Context(), h.deps.DB, &l); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, l)
}

// SetExpenseStatus moves an expense report along its state machine.
func (h *Handler) SetExpenseStatus(w http.ResponseWriter, r *http.Request) {
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
	var in statusIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	rep, err := h.deps.Store.SetExpenseStatus(r.Context(), h.deps.DB, entityID, id, ExpenseStatus(in.Status), in.RowVersion)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

type payExpenseIn struct {
	JournalID       int64 `json:"journal_id"`
	ExpenseAccount  int64 `json:"expense_account_id"`
	BankAccount     int64 `json:"bank_account_id"`
	RowVersion      int64 `json:"row_version"`
}

// PayExpense pays an approved report through the payout service: status
// flip and balanced ledger entry (debit expense, credit bank) commit
// atomically. A ledger failure now rolls back instead of best-effort revert.
func (h *Handler) PayExpense(w http.ResponseWriter, r *http.Request) {
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
	if h.deps.Finance == nil {
		writeErr(w, http.StatusServiceUnavailable, "hr: finance posting not wired")
		return
	}
	var in payExpenseIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if in.JournalID <= 0 || in.ExpenseAccount <= 0 || in.BankAccount <= 0 {
		writeErr(w, http.StatusUnprocessableEntity, "hr: journal, expense and bank accounts required")
		return
	}
	paid, err := h.svc.Pay(r.Context(), PayCmd{
		EntityID: entityID, ReportID: id, JournalID: in.JournalID,
		ExpenseAccount: in.ExpenseAccount, BankAccount: in.BankAccount, RowVersion: in.RowVersion})
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, paid)
}
// CreateSalary records a draft salary line (net must equal gross minus charges).
func (h *Handler) CreateSalary(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var s Salary
	if err := decode(r, &s); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	s.ID = 0
	s.EntityID = entityID
	s.Status = SalaryDraft
	if err := h.deps.Store.CreateSalary(r.Context(), h.deps.DB, &s); err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.hr.salary.created.v1", "salary", s.ID)
	writeJSON(w, http.StatusCreated, s)
}

// ListSalaries lists salary records, optionally filtered by user.
func (h *Handler) ListSalaries(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	_, _, user := page(r)
	list, err := h.deps.Store.SalariesOf(r.Context(), h.deps.DB, entityID, user)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// SetSalaryStatus moves a salary record along its state machine.
func (h *Handler) SetSalaryStatus(w http.ResponseWriter, r *http.Request) {
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
	var in statusIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	s, err := h.deps.Store.SetSalaryStatus(r.Context(), h.deps.DB, entityID, id, SalaryStatus(in.Status), in.RowVersion)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}
