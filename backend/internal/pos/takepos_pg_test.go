package pos

import (
	"context"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
	"github.com/YASSERRMD/forge-erp/backend/internal/sales"
)

// pgService seeds one sellable good + till on real Postgres and returns a
// transactional checkout service (Pool set → per-payload TxEntity).
func pgService(t *testing.T) (*Service, *PGStore, Session, int64) {
	t.Helper()
	ctx := context.Background()
	pool := pgtest.Pool(t)
	cst := catalog.NewPGStore(pool)
	sst := sales.NewPGStore(pool)
	pst := NewPGStore(pool)
	svc := NewService(pool, pst, cst, sst, 0, nil)

	p := &catalog.Product{EntityID: 1, SKU: "TP-PG-001", Name: "Widget",
		Type: catalog.ProductGoods, Unit: "unit", NetPrice: 1000, VATRateBps: 2000,
		Status: catalog.ProductActive, StockTracked: true}
	if err := cst.CreateProduct(ctx, pool, p); err != nil {
		t.Fatal(err)
	}
	w := &catalog.Warehouse{EntityID: 1, Code: "TPPG", Label: "Main", Status: 1}
	if err := cst.CreateWarehouse(ctx, pool, w); err != nil {
		t.Fatal(err)
	}
	if _, err := cst.AppendMovement(ctx, pool, &catalog.StockMovement{EntityID: 1,
		ProductID: p.ID, WarehouseID: w.ID, Qty: 100, UnitCost: 300,
		Reason: catalog.ReasonReceipt, Ref: "OPEN"}, false); err != nil {
		t.Fatal(err)
	}
	term := &Terminal{EntityID: 1, Code: "TP-PGT", Label: "Till", WarehouseID: w.ID, Status: TerminalActive}
	if err := pst.CreateTerminal(ctx, pool, term); err != nil {
		t.Fatal(err)
	}
	se := &Session{EntityID: 1, TerminalID: term.ID, Cashier: "ada", OpeningFloat: 5000}
	if err := pst.OpenSession(ctx, pool, se); err != nil {
		t.Fatal(err)
	}
	// Till sales need a real customer org (ferp_documents.org_id FK; no org
	// is seeded by migrations, so the test creates its own).
	var orgID int64
	if err := pool.QueryRow(ctx, `INSERT INTO ferp_organizations
		(entity_id, name, is_customer) VALUES (1, 'TakePOS PG', true) RETURNING id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	return svc, pst, *se, orgID
}

// TestPGQueuePersistReplay proves queue rows survive in Postgres and replay
// commits sale + sent-flip atomically, with double replay a no-op.
func TestPGQueuePersistReplay(t *testing.T) {
	ctx := context.Background()
	svc, pst, se, orgID := pgService(t)

	q := &QueuedSale{EntityID: 1, SessionID: se.ID, IdempotencyKey: "pg-k1",
		OrgID: orgID, Lines: []SaleLine{{ProductID: 1, Qty: 1}}, Method: PayCash, Tendered: 1200}
	if err := pst.EnqueueOffline(ctx, svc.Pool, q); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if q.ID == 0 || q.Status != QueueQueued {
		t.Fatalf("row=%+v", q)
	}
	// Persisted: readable by key, listed for drain.
	if got, err := pst.QueueByKey(ctx, svc.Pool, 1, "pg-k1"); err != nil || got.ID != q.ID {
		t.Fatalf("by key=%+v err=%v", got, err)
	}
	if drain, err := pst.QueuedDrain(ctx, svc.Pool, 1, se.ID); err != nil || len(drain) != 1 {
		t.Fatalf("drain=%v err=%v", drain, err)
	}

	res, err := svc.ReplayQueue(ctx, 1, se.ID)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(res.SaleIDs) != 1 || len(res.Failed) != 0 {
		t.Fatalf("replay=%+v", res)
	}
	sa, err := pst.SaleByID(ctx, svc.Pool, 1, res.SaleIDs[0])
	if err != nil || sa.TotalGross != 1200 {
		t.Fatalf("sale=%+v err=%v want gross 1200", sa, err)
	}
	if got, err := pst.QueueByKey(ctx, svc.Pool, 1, "pg-k1"); err != nil || got.Status != QueueSent || got.SentAt == nil {
		t.Fatalf("sent row=%+v err=%v", got, err)
	}

	// Double replay: no-op, no duplicate sale.
	res2, err := svc.ReplayQueue(ctx, 1, se.ID)
	if err != nil {
		t.Fatalf("replay2: %v", err)
	}
	if len(res2.SaleIDs) != 0 || len(res2.Failed) != 0 {
		t.Fatalf("replay2=%+v", res2)
	}
	if list, _ := pst.SalesOfSession(ctx, svc.Pool, se.ID); len(list) != 1 {
		t.Fatalf("sales=%d want 1", len(list))
	}

	// Poison payload persists as failed with attempts + error.
	bad := &QueuedSale{EntityID: 1, SessionID: se.ID, IdempotencyKey: "pg-bad",
		OrgID: orgID, Lines: []SaleLine{{ProductID: 999999, Qty: 1}}, Method: PayCash, Tendered: 5000}
	if err := pst.EnqueueOffline(ctx, svc.Pool, bad); err != nil {
		t.Fatalf("enqueue bad: %v", err)
	}
	res3, err := svc.ReplayQueue(ctx, 1, se.ID)
	if err != nil {
		t.Fatalf("replay3: %v", err)
	}
	if len(res3.SaleIDs) != 0 || len(res3.Failed) != 1 {
		t.Fatalf("replay3=%+v", res3)
	}
	if got, err := pst.QueueByKey(ctx, svc.Pool, 1, "pg-bad"); err != nil ||
		got.Status != QueueFailed || got.Attempts != 1 || got.LastError == "" {
		t.Fatalf("failed row=%+v err=%v", got, err)
	}
	if list, _ := pst.QueueList(ctx, svc.Pool, 1, se.ID, "failed"); len(list) != 1 {
		t.Fatalf("failed list=%d want 1", len(list))
	}
}

// TestPGCashReports proves payouts/counts persist and X/Z report the drawer
// expectation on Postgres (expected = 5000 + 2400 − 500 = 6900).
func TestPGCashReports(t *testing.T) {
	ctx := context.Background()
	svc, pst, se, orgID := pgService(t)

	// 2 x 1000 net + 20% VAT = 2400 gross, tendered 3000 (cash).
	if _, err := svc.Checkout(ctx, CheckoutCmd{EntityID: 1, SessionID: se.ID, OrgID: orgID,
		Lines: []SaleLine{{ProductID: 1, Qty: 2}}, Method: PayCash, Tendered: 3000}); err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if err := pst.RecordPayout(ctx, svc.Pool, &Payout{EntityID: 1, SessionID: se.ID, Amount: 500, Reason: "stamps"}); err != nil {
		t.Fatalf("payout: %v", err)
	}
	if pays, err := pst.PayoutsOfSession(ctx, svc.Pool, 1, se.ID); err != nil || len(pays) != 1 || pays[0].Amount != 500 {
		t.Fatalf("payouts=%v err=%v", pays, err)
	}

	c, err := svc.RecordCashCount(ctx, 1, se.ID, 7000)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if c.ExpectedCash != 6900 || c.Variance != 100 {
		t.Fatalf("count=%+v want expected 6900 variance 100", c)
	}
	if counts, err := pst.CountsOfSession(ctx, svc.Pool, 1, se.ID); err != nil || len(counts) != 1 {
		t.Fatalf("counts=%v err=%v", counts, err)
	}

	x, err := svc.XReport(ctx, 1, se.ID)
	if err != nil {
		t.Fatalf("X: %v", err)
	}
	if x.Totals.SaleCount != 1 || x.Totals.CashGross != 2400 || x.Totals.ExpectedCash != 6900 {
		t.Fatalf("x=%+v", x.Totals)
	}
	if x.Session.Status != SessionOpen {
		t.Fatal("X closed the session")
	}

	z, err := svc.ZReport(ctx, 1, se.ID, se.RowVersion)
	if err != nil {
		t.Fatalf("Z: %v", err)
	}
	if z.Kind != "Z" || z.Session.Status != SessionClosed {
		t.Fatalf("z=%+v", z)
	}
	// Closed on PG too: further checkout rolls back.
	if _, err := svc.Checkout(ctx, CheckoutCmd{EntityID: 1, SessionID: se.ID, OrgID: orgID,
		Lines: []SaleLine{{ProductID: 1, Qty: 1}}, Method: PayCash, Tendered: 1200}); err == nil {
		t.Fatal("checkout on closed session accepted")
	}
}
