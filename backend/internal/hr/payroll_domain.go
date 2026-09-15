package hr

import (
	"errors"
	"strings"
	"time"
)

// Payroll run status.
type PayrollRunStatus int16

const (
	PayrollRunDraft    PayrollRunStatus = 0
	PayrollRunPosted   PayrollRunStatus = 1
	PayrollRunCanceled PayrollRunStatus = -1
)

// PayrollRun groups salary lines for one pay period. Amounts live on the
// lines as GIVEN/recorded inputs: payroll calculation (jurisdictional
// tax/social-charge logic) is explicitly out of scope and never computed
// server-side — the API records caller-provided gross/charges/net subject
// to the net = gross - charges invariant.
type PayrollRun struct {
	ID          int64            `json:"id"`
	EntityID    int64            `json:"entity_id"`
	Label       string           `json:"label"`
	PeriodStart time.Time        `json:"period_start"`
	PeriodEnd   time.Time        `json:"period_end"`
	Status      PayrollRunStatus `json:"status"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
	RowVersion  int64            `json:"row_version"`
}

// Validate checks run invariants.
func (r PayrollRun) Validate() error {
	if r.EntityID <= 0 {
		return errors.New("hr: entity_id required")
	}
	if strings.TrimSpace(r.Label) == "" {
		return errors.New("hr: label required")
	}
	if r.PeriodStart.IsZero() || r.PeriodEnd.IsZero() {
		return errors.New("hr: period_start and period_end required")
	}
	if r.PeriodEnd.Before(r.PeriodStart) {
		return errors.New("hr: period_end before period_start")
	}
	return nil
}

// CanTransition reports whether a payroll-run status change is legal.
func (r PayrollRun) CanTransition(to PayrollRunStatus) bool {
	switch r.Status {
	case PayrollRunDraft:
		return to == PayrollRunPosted || to == PayrollRunCanceled
	default:
		return false
	}
}

// PayrollRunLine is one recorded salary cost inside a run. Amounts are
// GIVEN/recorded (calculation out of scope); net must equal gross-charges.
type PayrollRunLine struct {
	ID       int64  `json:"id"`
	EntityID int64  `json:"entity_id"`
	RunID    int64  `json:"run_id"`
	SalaryID *int64 `json:"salary_id"`
	Gross    int64  `json:"gross"`
	Charges  int64  `json:"charges"`
	Net      int64  `json:"net"` // must equal gross - charges
}

// Validate checks line invariants.
func (l PayrollRunLine) Validate() error {
	if l.EntityID <= 0 || l.RunID <= 0 {
		return errors.New("hr: entity_id and run_id required")
	}
	if l.Gross < 0 || l.Charges < 0 {
		return errors.New("hr: negative amounts")
	}
	if l.Charges > l.Gross {
		return errors.New("hr: charges exceed gross")
	}
	if l.Net != l.Gross-l.Charges {
		return errors.New("hr: net must equal gross minus charges")
	}
	return nil
}

// PayrollTotals sums a run's lines (all minor units).
func PayrollTotals(lines []PayrollRunLine) (gross, charges, net int64) {
	for _, l := range lines {
		gross += l.Gross
		charges += l.Charges
		net += l.Net
	}
	return gross, charges, net
}
