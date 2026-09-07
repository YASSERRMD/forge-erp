package pos

import (
	"context"
	"testing"
)

func TestMemoryTerminalSession(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	term := &Terminal{EntityID: 1, Code: "T1", Label: "Till", WarehouseID: 1, Status: TerminalActive}
	if err := m.CreateTerminal(ctx, term); err != nil {
		t.Fatalf("create terminal: %v", err)
	}
	if err := m.CreateTerminal(ctx, &Terminal{EntityID: 1, Code: "T1", Label: "dup", WarehouseID: 1}); err == nil {
		t.Error("duplicate code accepted")
	}
	se := &Session{EntityID: 1, TerminalID: term.ID, Cashier: "ada"}
	if err := m.OpenSession(ctx, se); err != nil {
		t.Fatalf("open: %v", err)
	}
	off := &Terminal{EntityID: 1, Code: "T2", Label: "Off", WarehouseID: 1, Status: TerminalInactive}
	if err := m.CreateTerminal(ctx, off); err != nil {
		t.Fatalf("create inactive: %v", err)
	}
	if err := m.OpenSession(ctx, &Session{EntityID: 1, TerminalID: off.ID, Cashier: "ada"}); err == nil {
		t.Error("session on inactive terminal accepted")
	}
	closed, err := m.CloseSession(ctx, se.ID, se.RowVersion)
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if closed.Status != SessionClosed || closed.ClosedAt == nil {
		t.Error("close did not stamp")
	}
	if _, err := m.CloseSession(ctx, se.ID, closed.RowVersion); err == nil {
		t.Error("double close accepted")
	}
	sa := &Sale{EntityID: 1, SessionID: se.ID, Ref: "POS-1", OrgID: 7,
		Lines: []SaleLine{{ProductID: 1, Qty: 1}}, TotalGross: 100,
		Method: PayCash, Tendered: 100, Status: SaleCompleted}
	if err := m.CreateSale(ctx, sa); err != nil {
		t.Fatalf("create sale: %v", err)
	}
	if list, _ := m.SalesOfSession(ctx, se.ID); len(list) != 1 {
		t.Fatalf("sales=%d want 1", len(list))
	}
}
