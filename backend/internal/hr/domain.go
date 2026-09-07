// Package hr implements people operations: leave requests (Dolibarr
// llx_holiday), expense reports (llx_expensereport + lines) and salary
// records (llx_salary). Employees are identity users referenced by login.
package hr

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Leave status (Dolibarr llx_holiday.statut).
type LeaveStatus int16

const (
	LeaveDraft     LeaveStatus = 0
	LeaveSubmitted LeaveStatus = 1
	LeaveApproved  LeaveStatus = 2
	LeaveRejected  LeaveStatus = -1
	LeaveCanceled  LeaveStatus = -2
)

// Leave types.
const (
	LeavePaid   = "paid"
	LeaveSick   = "sick"
	LeaveUnpaid = "unpaid"
)

// LeaveRequest is a time-off request for one employee.
type LeaveRequest struct {
	ID        int64       `json:"id"`
	EntityID  int64       `json:"entity_id"`
	UserLogin string      `json:"user_login"`
	Type      string      `json:"type"` // paid|sick|unpaid
	StartDate time.Time   `json:"start_date"`
	EndDate   time.Time   `json:"end_date"`
	Days      int64       `json:"days"` // inclusive calendar days, server-computed
	Status    LeaveStatus `json:"status"`
	Comment   string      `json:"comment"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
	RowVersion int64      `json:"row_version"`
}

// LeaveDays counts inclusive calendar days (intentional simplification:
// Dolibarr counts working days per country calendar — see DIFFERENCES).
func LeaveDays(start, end time.Time) int64 {
	s := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	e := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.UTC)
	return int64(e.Sub(s).Hours()/24) + 1
}

// Validate checks leave invariants.
func (l LeaveRequest) Validate() error {
	if l.EntityID <= 0 {
		return errors.New("hr: entity_id required")
	}
	if strings.TrimSpace(l.UserLogin) == "" {
		return errors.New("hr: user_login required")
	}
	switch l.Type {
	case LeavePaid, LeaveSick, LeaveUnpaid:
	default:
		return fmt.Errorf("hr: unknown leave type %q", l.Type)
	}
	if l.StartDate.IsZero() || l.EndDate.IsZero() {
		return errors.New("hr: start_date and end_date required")
	}
	if l.EndDate.Before(l.StartDate) {
		return errors.New("hr: end_date before start_date")
	}
	return nil
}

// CanTransition reports whether a leave status change is legal.
func (l LeaveRequest) CanTransition(to LeaveStatus) bool {
	switch l.Status {
	case LeaveDraft:
		return to == LeaveSubmitted || to == LeaveCanceled
	case LeaveSubmitted:
		return to == LeaveApproved || to == LeaveRejected || to == LeaveCanceled
	case LeaveApproved:
		return to == LeaveCanceled
	default:
		return false
	}
}

// Expense status (Dolibarr llx_expensereport.fk_statut).
type ExpenseStatus int16

const (
	ExpenseDraft     ExpenseStatus = 0
	ExpenseSubmitted ExpenseStatus = 1
	ExpenseApproved  ExpenseStatus = 2
	ExpensePaid      ExpenseStatus = 3
	ExpenseRejected  ExpenseStatus = -1
	ExpenseCanceled  ExpenseStatus = -2
)

// ExpenseReport is a claim header; totals derive from lines.
type ExpenseReport struct {
	ID         int64         `json:"id"`
	EntityID   int64         `json:"entity_id"`
	Ref        string        `json:"ref"` // unique per entity
	UserLogin  string        `json:"user_login"`
	Status     ExpenseStatus `json:"status"`
	Total      int64         `json:"total"` // minor units, server-computed
	CreatedAt  time.Time     `json:"created_at"`
	UpdatedAt  time.Time     `json:"updated_at"`
	RowVersion int64         `json:"row_version"`
}

// Validate checks report invariants.
func (r ExpenseReport) Validate() error {
	if r.EntityID <= 0 {
		return errors.New("hr: entity_id required")
	}
	if strings.TrimSpace(r.Ref) == "" {
		return errors.New("hr: ref required")
	}
	if strings.TrimSpace(r.UserLogin) == "" {
		return errors.New("hr: user_login required")
	}
	return nil
}

// CanTransition reports whether an expense status change is legal.
func (r ExpenseReport) CanTransition(to ExpenseStatus) bool {
	switch r.Status {
	case ExpenseDraft:
		return to == ExpenseSubmitted || to == ExpenseCanceled
	case ExpenseSubmitted:
		return to == ExpenseApproved || to == ExpenseRejected || to == ExpenseCanceled
	case ExpenseApproved:
		return to == ExpensePaid || to == ExpenseCanceled
	default:
		return false
	}
}

// ExpenseLine is one claimed cost (Dolibarr llx_expensereport_det).
type ExpenseLine struct {
	ID       int64     `json:"id"`
	EntityID int64     `json:"entity_id"`
	ReportID int64     `json:"report_id"`
	Date     time.Time `json:"date"`
	Label    string    `json:"label"`
	Amount   int64     `json:"amount"` // minor units, > 0
	VATBps   int64     `json:"vat_bps"`
}

// Validate checks line invariants.
func (l ExpenseLine) Validate() error {
	if l.EntityID <= 0 || l.ReportID <= 0 {
		return errors.New("hr: entity_id and report_id required")
	}
	if strings.TrimSpace(l.Label) == "" {
		return errors.New("hr: label required")
	}
	if l.Amount <= 0 {
		return errors.New("hr: amount must be positive")
	}
	if l.VATBps < 0 {
		return errors.New("hr: negative VAT rate")
	}
	if l.Date.IsZero() {
		return errors.New("hr: date required")
	}
	return nil
}

// Salary status.
type SalaryStatus int16

const (
	SalaryDraft     SalaryStatus = 0
	SalaryValidated SalaryStatus = 1
	SalaryPaid      SalaryStatus = 2
	SalaryCanceled  SalaryStatus = -1
)

var periodPattern = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

// Salary is one monthly payroll record (amounts in minor units).
type Salary struct {
	ID         int64        `json:"id"`
	EntityID   int64        `json:"entity_id"`
	UserLogin  string       `json:"user_login"`
	Period     string       `json:"period"` // YYYY-MM
	Gross      int64        `json:"gross"`
	Charges    int64        `json:"charges"`
	Net        int64        `json:"net"` // must equal gross - charges
	Status     SalaryStatus `json:"status"`
	CreatedAt  time.Time    `json:"created_at"`
	UpdatedAt  time.Time    `json:"updated_at"`
	RowVersion int64        `json:"row_version"`
}

// Validate checks salary invariants.
func (s Salary) Validate() error {
	if s.EntityID <= 0 {
		return errors.New("hr: entity_id required")
	}
	if strings.TrimSpace(s.UserLogin) == "" {
		return errors.New("hr: user_login required")
	}
	if !periodPattern.MatchString(s.Period) {
		return fmt.Errorf("hr: bad period %q, want YYYY-MM", s.Period)
	}
	if s.Gross < 0 || s.Charges < 0 {
		return errors.New("hr: negative amounts")
	}
	if s.Charges > s.Gross {
		return errors.New("hr: charges exceed gross")
	}
	if s.Net != s.Gross-s.Charges {
		return errors.New("hr: net must equal gross minus charges")
	}
	return nil
}

// CanTransition reports whether a salary status change is legal.
func (s Salary) CanTransition(to SalaryStatus) bool {
	switch s.Status {
	case SalaryDraft:
		return to == SalaryValidated || to == SalaryCanceled
	case SalaryValidated:
		return to == SalaryPaid || to == SalaryCanceled
	default:
		return false
	}
}
