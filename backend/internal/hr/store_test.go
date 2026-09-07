package hr

import (
	"context"
	"testing"
	"time"
)

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
	if err := m.CreateLeave(ctx, l); err != nil {
		t.Fatalf("create leave: %v", err)
	}
	if l.Days != 2 {
		t.Fatalf("days=%d want 2", l.Days)
	}
	if _, err := m.SetLeaveStatus(ctx, l.ID, LeaveApproved, l.RowVersion); err == nil {
		t.Error("draft→approved accepted")
	}
	upd, err := m.SetLeaveStatus(ctx, l.ID, LeaveSubmitted, l.RowVersion)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := m.SetLeaveStatus(ctx, upd.ID, LeaveApproved, upd.RowVersion); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if list, _ := m.LeavesOf(ctx, 1, "ada", 10, 0); len(list) != 1 {
		t.Fatalf("leaves=%d want 1", len(list))
	}
}

func TestMemoryExpenseFlow(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	r := &ExpenseReport{EntityID: 1, Ref: "EXP-1", UserLogin: "ada"}
	if err := m.CreateExpense(ctx, r); err != nil {
		t.Fatalf("create report: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for _, amt := range []int64{1200, 800} {
		if err := m.AddExpenseLine(ctx, &ExpenseLine{EntityID: 1, ReportID: r.ID,
			Date: now, Label: "meal", Amount: amt}); err != nil {
			t.Fatalf("add line: %v", err)
		}
	}
	if total, _ := m.ExpenseTotal(ctx, r.ID); total != 2000 {
		t.Fatalf("total=%d want 2000", total)
	}
	upd, err := m.SetExpenseStatus(ctx, r.ID, ExpenseSubmitted, r.RowVersion)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	upd, err = m.SetExpenseStatus(ctx, r.ID, ExpenseApproved, upd.RowVersion)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := m.AddExpenseLine(ctx, &ExpenseLine{EntityID: 1, ReportID: r.ID,
		Date: now, Label: "late", Amount: 100}); err == nil {
		t.Error("line on approved report accepted")
	}
	if _, err := m.SetExpenseStatus(ctx, r.ID, ExpensePaid, upd.RowVersion); err != nil {
		t.Fatalf("pay: %v", err)
	}
}

func TestMemorySalaryFlow(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	s := &Salary{EntityID: 1, UserLogin: "ada", Period: "2026-09", Gross: 500000, Charges: 110000, Net: 390000}
	if err := m.CreateSalary(ctx, s); err != nil {
		t.Fatalf("create salary: %v", err)
	}
	if err := m.CreateSalary(ctx, &Salary{EntityID: 1, UserLogin: "ada", Period: "2026-09",
		Gross: 1, Net: 1}); err == nil {
		t.Error("duplicate period accepted")
	}
	upd, err := m.SetSalaryStatus(ctx, s.ID, SalaryValidated, s.RowVersion)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if _, err := m.SetSalaryStatus(ctx, s.ID, SalaryPaid, upd.RowVersion); err != nil {
		t.Fatalf("pay: %v", err)
	}
	if list, _ := m.SalariesOf(ctx, 1, "ada"); len(list) != 1 {
		t.Fatalf("salaries=%d want 1", len(list))
	}
}
