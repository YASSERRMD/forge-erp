package pos

import (
	"context"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

func TestMemoryTerminalSession(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	term := &Terminal{EntityID: 1, Code: "T1", Label: "Till", WarehouseID: 1, Status: TerminalActive}
	if err := m.CreateTerminal(ctx, nil, term); err != nil {
		t.Fatalf("create terminal: %v", err)
	}
	if err := m.CreateTerminal(ctx, nil, &Terminal{EntityID: 1, Code: "T1", Label: "dup", WarehouseID: 1}); err == nil {
		t.Error("duplicate code accepted")
	}
	se := &Session{EntityID: 1, TerminalID: term.ID, Cashier: "ada"}
	if err := m.OpenSession(ctx, nil, se); err != nil {
		t.Fatalf("open: %v", err)
	}
	off := &Terminal{EntityID: 1, Code: "T2", Label: "Off", WarehouseID: 1, Status: TerminalInactive}
	if err := m.CreateTerminal(ctx, nil, off); err != nil {
		t.Fatalf("create inactive: %v", err)
	}
	if err := m.OpenSession(ctx, nil, &Session{EntityID: 1, TerminalID: off.ID, Cashier: "ada"}); err == nil {
		t.Error("session on inactive terminal accepted")
	}
	closed, err := m.CloseSession(ctx, nil, 1, se.ID, se.RowVersion)
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if closed.Status != SessionClosed || closed.ClosedAt == nil {
		t.Error("close did not stamp")
	}
	if _, err := m.CloseSession(ctx, nil, 1, se.ID, closed.RowVersion); err == nil {
		t.Error("double close accepted")
	}
	sa := &Sale{EntityID: 1, SessionID: se.ID, Ref: "POS-1", OrgID: 7,
		Lines: []SaleLine{{ProductID: 1, Qty: 1}}, TotalGross: 100,
		Method: PayCash, Tendered: 100, Status: SaleCompleted}
	if err := m.CreateSale(ctx, nil, sa); err != nil {
		t.Fatalf("create sale: %v", err)
	}
	if list, _ := m.SalesOfSession(ctx, nil, se.ID); len(list) != 1 {
		t.Fatalf("sales=%d want 1", len(list))
	}
}

func TestPGTerminalSession(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	st := NewPGStore(pool)
	term := &Terminal{EntityID: 1, Code: "PGT1", Label: "Till", WarehouseID: 1, Status: TerminalActive}
	if err := st.CreateTerminal(ctx, pool, term); err != nil {
		t.Fatalf("terminal: %v", err)
	}
	se := &Session{EntityID: 1, TerminalID: term.ID, Cashier: "ada"}
	if err := st.OpenSession(ctx, pool, se); err != nil {
		t.Fatalf("open: %v", err)
	}
	closed, err := st.CloseSession(ctx, pool, 1, se.ID, se.RowVersion)
	if err != nil || closed.Status != SessionClosed {
		t.Fatalf("close: %+v %v", closed, err)
	}
}

func TestMemoryCrossTenant(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	term := &Terminal{EntityID: 1, Code: "X1", Label: "Till", WarehouseID: 1, Status: TerminalActive}
	if err := m.CreateTerminal(ctx, nil, term); err != nil {
		t.Fatal(err)
	}
	if _, err := m.TerminalByID(ctx, nil, 2, term.ID); err == nil {
		t.Error("cross-tenant TerminalByID succeeded")
	}
	se := &Session{EntityID: 1, TerminalID: term.ID, Cashier: "ada"}
	if err := m.OpenSession(ctx, nil, se); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SessionByID(ctx, nil, 2, se.ID); err == nil {
		t.Error("cross-tenant SessionByID succeeded")
	}
	if _, err := m.CloseSession(ctx, nil, 2, se.ID, se.RowVersion); err == nil {
		t.Error("cross-tenant CloseSession succeeded")
	}
	sa := &Sale{EntityID: 1, SessionID: se.ID, Ref: "X-1", OrgID: 7,
		Lines: []SaleLine{{ProductID: 1, Qty: 1}}, TotalGross: 100,
		Method: PayCash, Tendered: 100, Status: SaleCompleted}
	if err := m.CreateSale(ctx, nil, sa); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SaleByID(ctx, nil, 2, sa.ID); err == nil {
		t.Error("cross-tenant SaleByID succeeded")
	}
	if _, err := m.VoidSale(ctx, nil, 2, sa.ID); err == nil {
		t.Error("cross-tenant VoidSale succeeded")
	}
	if _, err := m.MarkReturned(ctx, nil, 2, sa.ID); err == nil {
		t.Error("cross-tenant MarkReturned succeeded")
	}
}
