// Package pos_test hosts the Kernel 2 (hook bus) proof integration: a test
// module registered OUTSIDE the pos package vetoes a checkout and contributes
// a field to the hook result Data, with zero edits to sales/catalog
// internals. Memory fakes keep this PG-independent; the veto fires before any
// writes so there is nothing to roll back on the memory path (on PG the veto
// error propagates through platform.TxEntity, which rolls the transaction
// back — covered by the shared atomicity guarantee).
package pos_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/hook"
	"github.com/YASSERRMD/forge-erp/backend/internal/pos"
	"github.com/YASSERRMD/forge-erp/backend/internal/sales"
)

func hookSeed(t *testing.T, ctx context.Context, ledger *catalog.MemoryStore, store *pos.MemoryStore) pos.Session {
	t.Helper()
	p := &catalog.Product{EntityID: 1, SKU: "HOOK-001", Name: "Widget",
		Type: catalog.ProductGoods, Unit: "unit", NetPrice: 1000, VATRateBps: 2000,
		Status: catalog.ProductActive, StockTracked: true}
	if err := ledger.CreateProduct(ctx, nil, p); err != nil {
		t.Fatalf("create product: %v", err)
	}
	if _, err := ledger.AppendMovement(ctx, nil, &catalog.StockMovement{EntityID: 1,
		ProductID: p.ID, WarehouseID: 1, Qty: 10,
		Reason: catalog.ReasonReceipt, Ref: "OPEN"}, false); err != nil {
		t.Fatalf("seed stock: %v", err)
	}
	term := &pos.Terminal{EntityID: 1, Code: "HOOK-TILL", Label: "Hook till",
		WarehouseID: 1, Status: pos.TerminalActive}
	if err := store.CreateTerminal(ctx, nil, term); err != nil {
		t.Fatalf("create terminal: %v", err)
	}
	se := &pos.Session{EntityID: 1, TerminalID: term.ID, Cashier: "ada"}
	if err := store.OpenSession(ctx, nil, se); err != nil {
		t.Fatalf("open session: %v", err)
	}
	return *se
}

func hookInvoiceCount(t *testing.T, ctx context.Context, s *sales.MemoryStore) int {
	t.Helper()
	list, err := s.ListDocs(ctx, nil, 1, documents.TypeInvoice, 100, 0)
	if err != nil {
		t.Fatalf("list invoices: %v", err)
	}
	return len(list)
}

// TestHookVetoesCheckout is the Kernel 2 DoD: an external test module (this
// file — outside pos's non-test source, touching neither sales nor catalog)
// vetoes a checkout AND contributes a field to the hook result Data.
func TestHookVetoesCheckout(t *testing.T) {
	ctx := context.Background()
	ledger := catalog.NewMemoryStore()
	sal := sales.NewMemoryStore()
	store := pos.NewMemoryStore()
	se := hookSeed(t, ctx, ledger, store)

	bus := hook.NewBus()
	// Test module: runs before checkout writes, sees the caller's entity.
	veto := fmt.Errorf("testmodule: blocked tender: %w", platform.ErrValidation)
	bus.Register(hook.POSCheckoutValidate, 10, func(_ context.Context, _ pgx.Tx, _ hook.Context, subject any) (hook.Result, error) {
		sub, ok := subject.(*pos.CheckoutHookSubject)
		if !ok {
			return hook.Result{}, fmt.Errorf("testmodule: subject %T: %w", subject, platform.ErrValidation)
		}
		if sub.Cmd.EntityID != 1 {
			return hook.Result{}, fmt.Errorf("testmodule: entity=%d want 1: %w", sub.Cmd.EntityID, platform.ErrValidation)
		}
		return hook.Result{Veto: veto, Data: map[string]any{"hook_tag": "testmodule-veto"}}, nil
	})

	svc := pos.NewService(nil, store, ledger, sal, 0, nil)
	svc.Hooks = bus

	// 2 x 1000 net + 20% VAT = 2400 gross; tender 3000 would succeed untouched.
	_, err := svc.Checkout(ctx, pos.CheckoutCmd{EntityID: 1, SessionID: se.ID, OrgID: 7,
		Lines: []pos.SaleLine{{ProductID: 1, Qty: 2}}, Method: pos.PayCash, Tendered: 3000})
	if !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("checkout err=%v want veto wrapping platform.ErrValidation", err)
	}
	if !errors.Is(err, veto) {
		t.Fatalf("checkout err=%v want veto identity", err)
	}

	// Rollback proof: no till sale, no invoice, stock untouched.
	if got, err := store.SalesOfSession(ctx, nil, se.ID); err != nil || len(got) != 0 {
		t.Fatalf("sales=%v err=%v want zero sales after veto", got, err)
	}
	if n := hookInvoiceCount(t, ctx, sal); n != 0 {
		t.Fatalf("invoices=%d want 0 after veto", n)
	}
	lvl, err := ledger.Level(ctx, nil, 1, 1)
	if err != nil || lvl.Qty != 10 {
		t.Fatalf("stock=%+v err=%v want qty 10 after veto", lvl, err)
	}

	// The same module's Data contribution is visible on the hook result.
	res, err := bus.Execute(ctx, nil, hook.POSCheckoutValidate,
		&pos.CheckoutHookSubject{Cmd: pos.CheckoutCmd{EntityID: 1}})
	if !errors.Is(err, veto) {
		t.Fatalf("execute err=%v want veto", err)
	}
	if res.Data["hook_tag"] != "testmodule-veto" {
		t.Fatalf("data=%v want hook_tag=testmodule-veto", res.Data)
	}
}

// TestHookMutateAdjustsCheckout proves Mutate flows through pos: the hook
// defaults an anonymous till sale to org 7, turning a rejection into a sale.
func TestHookMutateAdjustsCheckout(t *testing.T) {
	ctx := context.Background()
	ledger := catalog.NewMemoryStore()
	sal := sales.NewMemoryStore()
	store := pos.NewMemoryStore()
	se := hookSeed(t, ctx, ledger, store)

	bus := hook.NewBus()
	bus.Register(hook.POSCheckoutValidate, 0, func(_ context.Context, _ pgx.Tx, _ hook.Context, subject any) (hook.Result, error) {
		return hook.Result{
			Mutate: func(v any) error {
				sub := v.(*pos.CheckoutHookSubject)
				if sub.Cmd.OrgID == 0 {
					sub.Cmd.OrgID = 7
				}
				return nil
			},
			Data: map[string]any{"defaulted_org": true},
		}, nil
	})

	// WalkinOrg 0 rejects anonymous sales — the hook repairs the command.
	svc := pos.NewService(nil, store, ledger, sal, 0, nil)
	svc.Hooks = bus

	// 1 x 1000 net + 20% VAT = 1200 gross (int64 minor units, untouched).
	rec, err := svc.Checkout(ctx, pos.CheckoutCmd{EntityID: 1, SessionID: se.ID,
		Lines: []pos.SaleLine{{ProductID: 1, Qty: 1}}, Method: pos.PayCash, Tendered: 1200})
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if rec.OrgID != 7 {
		t.Fatalf("org=%d want 7 from hook mutate", rec.OrgID)
	}
	if rec.TotalGross != 1200 || rec.Change != 0 {
		t.Fatalf("sale=%+v want gross 1200 change 0", rec)
	}
}
