package hr

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

func isNotFound(err error) bool { return errors.Is(err, identity.ErrNotFound) }

func TestLeaveDaysAndTransitions(t *testing.T) {
	s := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	e := time.Date(2026, 9, 8, 18, 0, 0, 0, time.UTC)
	if d := LeaveDays(s, e); d != 3 {
		t.Fatalf("days=%d want 3", d)
	}
	l := LeaveRequest{Status: LeaveDraft}
	if !l.CanTransition(LeaveSubmitted) || l.CanTransition(LeaveApproved) {
		t.Error("draft leave transitions wrong")
	}
	if (LeaveRequest{Type: "sabbatical"}.Validate() == nil) {
		t.Error("unknown leave type accepted")
	}
	if (Salary{EntityID: 1, UserLogin: "a", Period: "2026-13", Gross: 1, Net: 1}.Validate() == nil) {
		t.Error("bad period accepted")
	}
	if (Salary{EntityID: 1, UserLogin: "a", Period: "2026-09", Gross: 100, Charges: 20, Net: 70}.Validate() == nil) {
		t.Error("unbalanced salary accepted")
	}
}

func TestMemoryLeaveFlow(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	s := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	e := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	l := &LeaveRequest{EntityID: 1, UserLogin: "ada", Type: LeavePaid, StartDate: s, EndDate: e, Comment: "rest"}
	if err := m.CreateLeave(ctx, nil, l); err != nil {
		t.Fatalf("create leave: %v", err)
	}
	if l.Days != 2 {
		t.Fatalf("days=%d want 2", l.Days)
	}
	if _, err := m.SetLeaveStatus(ctx, nil, 1, l.ID, LeaveApproved, l.RowVersion); err == nil {
		t.Error("draft→approved accepted")
	}
	upd, err := m.SetLeaveStatus(ctx, nil, 1, l.ID, LeaveSubmitted, l.RowVersion)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := m.SetLeaveStatus(ctx, nil, 1, upd.ID, LeaveApproved, upd.RowVersion); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if list, _ := m.LeavesOf(ctx, nil, 1, "ada", 10, 0); len(list) != 1 {
		t.Fatalf("leaves=%d want 1", len(list))
	}
}

func TestMemoryExpenseFlow(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	r := &ExpenseReport{EntityID: 1, Ref: "EXP-1", UserLogin: "ada"}
	if err := m.CreateExpense(ctx, nil, r); err != nil {
		t.Fatalf("create report: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for _, amt := range []int64{1200, 800} {
		if err := m.AddExpenseLine(ctx, nil, &ExpenseLine{EntityID: 1, ReportID: r.ID,
			Date: now, Label: "meal", Amount: amt}); err != nil {
			t.Fatalf("add line: %v", err)
		}
	}
	if total, _ := m.ExpenseTotal(ctx, nil, 1, r.ID); total != 2000 {
		t.Fatalf("total=%d want 2000", total)
	}
	upd, err := m.SetExpenseStatus(ctx, nil, 1, r.ID, ExpenseSubmitted, r.RowVersion)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	upd, err = m.SetExpenseStatus(ctx, nil, 1, r.ID, ExpenseApproved, upd.RowVersion)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := m.AddExpenseLine(ctx, nil, &ExpenseLine{EntityID: 1, ReportID: r.ID,
		Date: now, Label: "late", Amount: 100}); err == nil {
		t.Error("line on approved report accepted")
	}
	if _, err := m.SetExpenseStatus(ctx, nil, 1, r.ID, ExpensePaid, upd.RowVersion); err != nil {
		t.Fatalf("pay: %v", err)
	}
}

func TestMemorySalaryFlow(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	s := &Salary{EntityID: 1, UserLogin: "ada", Period: "2026-09", Gross: 500000, Charges: 110000, Net: 390000}
	if err := m.CreateSalary(ctx, nil, s); err != nil {
		t.Fatalf("create salary: %v", err)
	}
	if err := m.CreateSalary(ctx, nil, &Salary{EntityID: 1, UserLogin: "ada", Period: "2026-09",
		Gross: 1, Net: 1}); err == nil {
		t.Error("duplicate period accepted")
	}
	upd, err := m.SetSalaryStatus(ctx, nil, 1, s.ID, SalaryValidated, s.RowVersion)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if _, err := m.SetSalaryStatus(ctx, nil, 1, s.ID, SalaryPaid, upd.RowVersion); err != nil {
		t.Fatalf("pay: %v", err)
	}
	if list, _ := m.SalariesOf(ctx, nil, 1, "ada"); len(list) != 1 {
		t.Fatalf("salaries=%d want 1", len(list))
	}
}

func TestPGLeaveFlow(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	st := NewPGStore(pool)
	s := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	e := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	l := &LeaveRequest{EntityID: 1, UserLogin: "ada", Type: LeavePaid, StartDate: s, EndDate: e}
	if err := st.CreateLeave(ctx, pool, l); err != nil {
		t.Fatalf("leave: %v", err)
	}
	if l.Days != 2 {
		t.Fatalf("days=%d", l.Days)
	}
	if _, err := st.SetLeaveStatus(ctx, pool, 1, l.ID, LeaveSubmitted, l.RowVersion); err != nil {
		t.Fatalf("submit: %v", err)
	}
}

func TestCrossTenantIsolation(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	s := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	e := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	l := &LeaveRequest{EntityID: 1, UserLogin: "ada", Type: LeavePaid, StartDate: s, EndDate: e}
	if err := m.CreateLeave(ctx, nil, l); err != nil {
		t.Fatalf("create leave: %v", err)
	}
	if _, err := m.SetLeaveStatus(ctx, nil, 2, l.ID, LeaveSubmitted, l.RowVersion); err == nil {
		t.Fatal("cross-tenant SetLeaveStatus accepted")
	} else if !isNotFound(err) {
		t.Fatalf("cross-tenant SetLeaveStatus err=%v want not-found", err)
	}
	r := &ExpenseReport{EntityID: 1, Ref: "EXP-X", UserLogin: "ada"}
	if err := m.CreateExpense(ctx, nil, r); err != nil {
		t.Fatalf("create expense: %v", err)
	}
	if _, err := m.SetExpenseStatus(ctx, nil, 2, r.ID, ExpenseSubmitted, r.RowVersion); err == nil {
		t.Fatal("cross-tenant SetExpenseStatus accepted")
	} else if !isNotFound(err) {
		t.Fatalf("cross-tenant SetExpenseStatus err=%v want not-found", err)
	}
	if _, err := m.ExpenseTotal(ctx, nil, 2, r.ID); err == nil {
		t.Fatal("cross-tenant ExpenseTotal accepted")
	} else if !isNotFound(err) {
		t.Fatalf("cross-tenant ExpenseTotal err=%v want not-found", err)
	}
	sal := &Salary{EntityID: 1, UserLogin: "ada", Period: "2026-09", Gross: 1000, Charges: 200, Net: 800}
	if err := m.CreateSalary(ctx, nil, sal); err != nil {
		t.Fatalf("create salary: %v", err)
	}
	if _, err := m.SetSalaryStatus(ctx, nil, 2, sal.ID, SalaryValidated, sal.RowVersion); err == nil {
		t.Fatal("cross-tenant SetSalaryStatus accepted")
	} else if !isNotFound(err) {
		t.Fatalf("cross-tenant SetSalaryStatus err=%v want not-found", err)
	}
}
