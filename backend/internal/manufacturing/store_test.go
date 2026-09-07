package manufacturing

import (
	"context"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
)

func TestExplodeAndTransitions(t *testing.T) {
	lines := []BOMLine{
		{ComponentID: 2, Qty: 3},
		{ComponentID: 3, Qty: 1},
	}
	reqs, err := Explode(lines, 10)
	if err != nil {
		t.Fatalf("explode: %v", err)
	}
	if len(reqs) != 2 || reqs[0].Qty != 30 || reqs[1].Qty != 10 {
		t.Fatalf("requirements=%+v want 30/10", reqs)
	}
	if _, err := Explode(lines, 0); err == nil {
		t.Error("zero qty accepted")
	}
	if (BOM{Status: BOMActive}.CanTransition(BOMDraft)) {
		t.Error("active→draft accepted")
	}
	mo := ManufacturingOrder{Status: MOValidated}
	if !mo.CanTransition(MOInProgress) || mo.CanTransition(MOProduced) {
		t.Error("validated MO transitions wrong")
	}
	if (BOMLine{EntityID: 1, BOMID: 1, ComponentID: 9, Qty: 1}.Validate(9) == nil) {
		t.Error("self-reference accepted")
	}
}

func seedLedger(t *testing.T, ctx context.Context, ledger *catalog.MemoryStore) {
	t.Helper()
	for _, mv := range []catalog.StockMovement{
		{EntityID: 1, ProductID: 2, WarehouseID: 1, Qty: 100, Reason: catalog.ReasonReceipt, Ref: "OPEN"},
		{EntityID: 1, ProductID: 3, WarehouseID: 1, Qty: 50, Reason: catalog.ReasonReceipt, Ref: "OPEN"},
	} {
		m := mv
		if _, err := ledger.AppendMovement(ctx, &m, false); err != nil {
			t.Fatalf("seed movement: %v", err)
		}
	}
}

func TestProduceBalances(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	ledger := catalog.NewMemoryStore()
	seedLedger(t, ctx, ledger)

	bom := &BOM{EntityID: 1, Ref: "BOM-1", ProductID: 1, Label: "Widget"}
	if err := m.CreateBOM(ctx, bom); err != nil {
		t.Fatalf("create BOM: %v", err)
	}
	for _, l := range []BOMLine{
		{EntityID: 1, BOMID: bom.ID, ComponentID: 2, Qty: 3},
		{EntityID: 1, BOMID: bom.ID, ComponentID: 3, Qty: 1},
	} {
		l := l
		if err := m.AddLine(ctx, &l); err != nil {
			t.Fatalf("add line: %v", err)
		}
	}
	if err := m.AddLine(ctx, &BOMLine{EntityID: 1, BOMID: bom.ID, ComponentID: 2, Qty: 1}); err == nil {
		t.Error("duplicate component accepted")
	}
	if err := m.AddLine(ctx, &BOMLine{EntityID: 1, BOMID: bom.ID, ComponentID: 1, Qty: 1}); err == nil {
		t.Error("self-reference accepted")
	}

	mo := &ManufacturingOrder{EntityID: 1, Ref: "MO-1", BOMID: bom.ID, ProductID: 1, WarehouseID: 1, Qty: 10}
	if err := m.CreateMO(ctx, mo); err != nil {
		t.Fatalf("create MO: %v", err)
	}
	bad := &ManufacturingOrder{EntityID: 1, Ref: "MO-2", BOMID: bom.ID, ProductID: 2, WarehouseID: 1, Qty: 1}
	if err := m.CreateMO(ctx, bad); err == nil {
		t.Error("product/BOM mismatch accepted")
	}
	upd, err := m.SetMOStatus(ctx, mo.ID, MOValidated, mo.RowVersion)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	upd, err = m.SetMOStatus(ctx, mo.ID, MOInProgress, upd.RowVersion)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	lines, _ := m.LinesOf(ctx, bom.ID)
	plan, err := PostProduce(ctx, upd, lines, ledger)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}
	if len(plan.Consumes) != 2 || plan.Produce.Qty != 10 {
		t.Fatalf("plan=%+v", plan)
	}
	if _, err := m.MarkProduced(ctx, mo.ID, upd.RowVersion); err != nil {
		t.Fatalf("mark produced: %v", err)
	}
	lvl2, _ := ledger.Level(ctx, 2, 1)
	lvl3, _ := ledger.Level(ctx, 3, 1)
	lvl1, _ := ledger.Level(ctx, 1, 1)
	if lvl2.Qty != 70 || lvl3.Qty != 40 || lvl1.Qty != 10 {
		t.Fatalf("levels=%d/%d/%d want 70/40/10", lvl2.Qty, lvl3.Qty, lvl1.Qty)
	}
}

func TestProduceInsufficientStock(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	ledger := catalog.NewMemoryStore()
	seedLedger(t, ctx, ledger)

	bom := &BOM{EntityID: 1, Ref: "BOM-2", ProductID: 1, Label: "Big widget"}
	if err := m.CreateBOM(ctx, bom); err != nil {
		t.Fatalf("create BOM: %v", err)
	}
	l := &BOMLine{EntityID: 1, BOMID: bom.ID, ComponentID: 2, Qty: 3}
	if err := m.AddLine(ctx, l); err != nil {
		t.Fatalf("add line: %v", err)
	}
	mo := &ManufacturingOrder{EntityID: 1, Ref: "MO-9", BOMID: bom.ID, ProductID: 1, WarehouseID: 1, Qty: 100}
	if err := m.CreateMO(ctx, mo); err != nil {
		t.Fatalf("create MO: %v", err)
	}
	upd, _ := m.SetMOStatus(ctx, mo.ID, MOValidated, mo.RowVersion)
	upd, _ = m.SetMOStatus(ctx, mo.ID, MOInProgress, upd.RowVersion)
	lines, _ := m.LinesOf(ctx, bom.ID)
	if _, err := PostProduce(ctx, upd, lines, ledger); err == nil {
		t.Error("over-consumption accepted")
	}
	// Draft MO cannot produce.
	draft := *mo
	draft.Status = MODraft
	if _, err := PostProduce(ctx, draft, lines, ledger); err == nil {
		t.Error("produce from draft accepted")
	}
}

func TestListMOsMemory(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	bom := &BOM{EntityID: 1, Ref: "BOM-L", ProductID: 1, Label: "L"}
	if err := m.CreateBOM(ctx, bom); err != nil {
		t.Fatalf("bom: %v", err)
	}
	mo := &ManufacturingOrder{EntityID: 1, Ref: "MO-L", BOMID: bom.ID, ProductID: 1, WarehouseID: 1, Qty: 2}
	if err := m.CreateMO(ctx, mo); err != nil {
		t.Fatalf("mo: %v", err)
	}
	list, err := m.ListMOs(ctx, 1, 10, 0)
	if err != nil || len(list) != 1 || list[0].Ref != "MO-L" {
		t.Fatalf("mos=%+v err=%v", list, err)
	}
}
