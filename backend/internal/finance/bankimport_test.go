package finance

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestParseBankCSV(t *testing.T) {
	in := "ref,date,label,amount\nR1,2026-03-01,invoice 1,120.50\nR2,15/03/2026,fee,-5,20\n"
	_ = in
	// Note: "-5,20" contains a comma, so quote it for valid CSV.
	in = "ref,date,label,amount\nR1,2026-03-01,invoice 1,120.50\nR2,15/03/2026,fee,\"-5,20\"\n"
	lines, err := ParseBankCSV(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("lines = %d", len(lines))
	}
	if lines[0].Amount != 12050 || lines[0].Ref != "R1" {
		t.Fatalf("line0 = %+v", lines[0])
	}
	if lines[1].Amount != -520 {
		t.Fatalf("line1 amount = %d", lines[1].Amount)
	}
	if lines[1].ValueDate.Day() != 15 {
		t.Fatalf("line1 date = %v", lines[1].ValueDate)
	}
	if _, err := ParseBankCSV(strings.NewReader("label,amount\nx,1.00")); err == nil {
		t.Error("missing date column accepted")
	}
	if _, err := ParseBankCSV(strings.NewReader("date,amount\n2026-01-01,1.234")); err == nil {
		t.Error("3-decimal amount accepted")
	}
}

const camtSample = `<?xml version="1.0" encoding="UTF-8"?>
<Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.053.001.02">
  <BkToCstmrStmt>
    <Stmt>
      <Id>STMT-1</Id>
      <Ntry>
        <Amt Ccy="EUR">120.50</Amt>
        <CdtDbtInd>CRDT</CdtDbtInd>
        <BookgDt><Dt>2026-03-01</Dt></BookgDt>
        <ValDt><Dt>2026-03-02</Dt></ValDt>
        <NtryRef>REF-1</NtryRef>
        <AddtlNtryInf>invoice 1</AddtlNtryInf>
      </Ntry>
      <Ntry>
        <Amt Ccy="EUR">5.20</Amt>
        <CdtDbtInd>DBIT</CdtDbtInd>
        <BookgDt><Dt>2026-03-03</Dt></BookgDt>
        <AcctSvcrRef>REF-2</AcctSvcrRef>
      </Ntry>
    </Stmt>
  </BkToCstmrStmt>
</Document>`

func TestParseCAMT053(t *testing.T) {
	lines, err := ParseCAMT053(strings.NewReader(camtSample))
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("lines = %d", len(lines))
	}
	if lines[0].Amount != 12050 || lines[0].Ref != "REF-1" {
		t.Fatalf("line0 = %+v", lines[0])
	}
	if got := lines[0].ValueDate; got.Day() != 2 {
		t.Fatalf("ValDt not preferred: %v", got)
	}
	if lines[1].Amount != -520 || lines[1].Ref != "REF-2" {
		t.Fatalf("line1 = %+v", lines[1])
	}
	if _, err := ParseCAMT053(strings.NewReader("<Document/>")); err == nil {
		t.Error("empty camt accepted")
	}
}

func TestImportDedupeAndStatement(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	ba := &BankAccount{EntityID: 1, Code: "B1", Label: "Main"}
	if err := st.CreateBankAccount(ctx, nil, ba); err != nil {
		t.Fatal(err)
	}
	day := func(d int) time.Time { return time.Date(2026, 3, d, 0, 0, 0, 0, time.UTC) }
	lines := []ImportLine{
		{Ref: "R1", Label: "in", Amount: 10000, ValueDate: day(2)},
		{Ref: "R2", Label: "out", Amount: -3000, ValueDate: day(1)},
		{Ref: "", Label: "noref", Amount: 500, ValueDate: day(3)},
	}
	imp, skip, err := st.ImportTransactions(ctx, nil, 1, ba.ID, lines)
	if err != nil || imp != 3 || skip != 0 {
		t.Fatalf("import = %d/%d err=%v", imp, skip, err)
	}
	imp, skip, err = st.ImportTransactions(ctx, nil, 1, ba.ID, lines)
	if err != nil || imp != 1 || skip != 2 {
		t.Fatalf("reimport = %d/%d err=%v (ref-less line re-imports, refs skip)", imp, skip, err)
	}
	stmt, err := st.BankStatement(ctx, nil, ba.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stmt) != 4 {
		t.Fatalf("statement lines = %d", len(stmt))
	}
	// Ordered by date: -3000, +10000, +500, +500 → balances -3000, 7000, 7500, 8000.
	want := []int64{-3000, 7000, 7500, 8000}
	for i, w := range want {
		if stmt[i].Balance != w {
			t.Fatalf("line %d balance = %d want %d", i, stmt[i].Balance, w)
		}
	}
}

func TestTransferPaired(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	a := &BankAccount{EntityID: 1, Code: "A", Label: "A"}
	b := &BankAccount{EntityID: 1, Code: "B", Label: "B"}
	_ = st.CreateBankAccount(ctx, nil, a)
	_ = st.CreateBankAccount(ctx, nil, b)
	svc := NewService(nil, st)
	cmd := TransferCmd{EntityID: 1, FromAccountID: a.ID, ToAccountID: b.ID,
		Amount: 2500, Label: "sweep", Ref: "T1", ValueDate: time.Now().UTC()}
	if err := svc.Transfer(ctx, nil, cmd); err != nil {
		t.Fatal(err)
	}
	ba, _ := st.AccountBalance(ctx, nil, a.ID)
	bb, _ := st.AccountBalance(ctx, nil, b.ID)
	if ba != -2500 || bb != 2500 {
		t.Fatalf("balances = %d/%d", ba, bb)
	}
	if err := svc.Transfer(ctx, nil, cmd); err != nil {
		t.Fatal(err) // distinct /out /in refs per leg; same cmd re-runs (no dedupe clash)
	}
	bad := cmd
	bad.FromAccountID = bad.ToAccountID
	if err := svc.Transfer(ctx, nil, bad); err == nil {
		t.Error("same-account transfer accepted")
	}
	bad = cmd
	bad.Amount = 0
	if err := svc.Transfer(ctx, nil, bad); err == nil {
		t.Error("zero transfer accepted")
	}
	bad = cmd
	bad.ToAccountID = 9999
	if err := svc.Transfer(ctx, nil, bad); err == nil {
		t.Error("missing account transfer accepted")
	}
}

func TestMatchTransactions(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 3, d, 12, 0, 0, 0, time.UTC) }
	txs := []BankTransaction{
		{ID: 1, Amount: 10000, ValueDate: day(2)},
		{ID: 2, Amount: 10000, ValueDate: day(9)},
		{ID: 3, Amount: -500, ValueDate: day(3), Reconciled: true},
	}
	lines := []ImportLine{
		{Ref: "A", Amount: 10000, ValueDate: day(3)},  // matches tx 1 (1d window)
		{Ref: "B", Amount: 10000, ValueDate: day(20)}, // outside window
		{Ref: "C", Amount: -500, ValueDate: day(3)},   // tx reconciled → unmatched
	}
	m := MatchTransactions(txs, lines, 48*time.Hour)
	if m[0].Status != "matched" || m[0].TransactionID != 1 {
		t.Fatalf("m0 = %+v", m[0])
	}
	if m[1].Status != "unmatched" || m[2].Status != "unmatched" {
		t.Fatalf("m1/m2 = %+v/%+v", m[1], m[2])
	}
}
