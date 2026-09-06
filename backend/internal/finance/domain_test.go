package finance

import (
	"testing"
	"time"
)

func TestEntryValidate(t *testing.T) {
	ok := Entry{EntityID: 1, JournalID: 2, Ref: "VEN-1", Date: time.Now().UTC(),
		Lines: []EntryLine{
			{AccountID: 411, Label: "Customer", Debit: 1200},
			{AccountID: 707, Label: "Sales", Credit: 1000},
			{AccountID: 4457, Label: "VAT", Credit: 200},
		}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := ok
	bad.Lines = bad.Lines[:2] // 1200 dr vs 1000 cr
	if err := bad.Validate(); err == nil {
		t.Error("unbalanced entry accepted")
	}
	one := ok
	one.Lines = one.Lines[:1]
	if err := one.Validate(); err == nil {
		t.Error("single-leg entry accepted")
	}
	both := ok
	both.Lines[0].Credit = 5
	if err := both.Validate(); err == nil {
		t.Error("two-sided leg accepted")
	}
}

func TestChainVerify(t *testing.T) {
	now := time.Now().UTC()
	mk := func(prev string, ref string) Entry {
		e := Entry{ID: 1, EntityID: 1, JournalID: 1, Ref: ref, Date: now,
			Lines: []EntryLine{{AccountID: 1, Debit: 100}, {AccountID: 2, Credit: 100}},
			PrevHash: prev}
		e.ChainHash = Chain(prev, e.JournalID, e.Ref, e.Date, e.Lines)
		return e
	}
	e1 := mk(GenesisHash, "A")
	e2 := mk(e1.ChainHash, "B")
	if err := VerifyChain([]Entry{e1, e2}); err != nil {
		t.Fatal(err)
	}
	tampered := e2
	tampered.Lines[0].Debit = 999
	if err := VerifyChain([]Entry{e1, tampered}); err == nil {
		t.Error("tampered entry passed")
	}
	reordered := e2
	reordered.PrevHash = GenesisHash
	if err := VerifyChain([]Entry{e1, reordered}); err == nil {
		t.Error("broken link passed")
	}
}

func TestFiscalYearLock(t *testing.T) {
	fy := FiscalYear{ID: 1, Label: "2026", Locked: true,
		StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)}
	if !fy.Contains(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)) {
		t.Error("in-year date rejected")
	}
	if fy.Contains(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Error("out-of-year date accepted")
	}
}

func TestBuildSchedule(t *testing.T) {
	sched := BuildSchedule(120000, 600, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 12)
	if len(sched) != 12 {
		t.Fatalf("periods = %d", len(sched))
	}
	var principal int64
	for _, l := range sched {
		principal += l.Principal
	}
	if principal != 120000 {
		t.Fatalf("principal sums to %d", principal)
	}
}
