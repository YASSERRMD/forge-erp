package manufacturing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

func jsonDecode(rec *httptest.ResponseRecorder, v any) {
	if err := json.NewDecoder(rec.Body).Decode(v); err != nil {
		panic(fmt.Sprintf("jsonDecode: %v", err))
	}
}

func mustWorkstation(t *testing.T, ctx context.Context, st Store, code string, capMin int64) Workstation {
	t.Helper()
	w := &Workstation{EntityID: 1, Code: code, Label: code + " station", DailyCapacityMin: capMin, Status: WorkstationActive}
	if err := st.CreateWorkstation(ctx, nil, w); err != nil {
		t.Fatalf("create workstation %s: %v", code, err)
	}
	return *w
}

func TestScheduleVectors(t *testing.T) {
	start := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	routing := []BOMOperation{
		{EntityID: 1, BOMID: 7, Seq: 2, WorkstationID: 20, RunMinutesPerUnit: 10, SetupMinutes: 0},
		{EntityID: 1, BOMID: 7, Seq: 1, WorkstationID: 10, RunMinutesPerUnit: 5, SetupMinutes: 30},
	}
	caps := map[int64]int64{10: 480, 20: 480}

	t.Run("fit sequential", func(t *testing.T) {
		ops, err := ScheduleOperations(routing, 10, start, caps, nil)
		if err != nil {
			t.Fatalf("schedule: %v", err)
		}
		// Op1: 30+50=80min day 1; op2: 100min day 2 (chained).
		if len(ops) != 2 || ops[0].Seq != 1 || ops[1].Seq != 2 {
			t.Fatalf("seq order wrong: %+v", ops)
		}
		if ops[0].PlannedMinutes != 80 || ops[1].PlannedMinutes != 100 {
			t.Fatalf("planned=%d/%d want 80/100", ops[0].PlannedMinutes, ops[1].PlannedMinutes)
		}
		if !ops[0].ScheduledStart.Equal(start) || !ops[1].ScheduledStart.Equal(start.AddDate(0, 0, 1)) {
			t.Fatalf("starts=%v/%v", ops[0].ScheduledStart, ops[1].ScheduledStart)
		}
		if ops[0].Overloaded || ops[1].Overloaded {
			t.Fatalf("unexpected overload: %+v", ops)
		}
	})

	t.Run("multi-day span", func(t *testing.T) {
		big := []BOMOperation{{EntityID: 1, BOMID: 7, Seq: 1, WorkstationID: 10, RunMinutesPerUnit: 100}}
		ops, err := ScheduleOperations(big, 10, start, caps, nil) // 1000min on 480 cap
		if err != nil {
			t.Fatalf("schedule: %v", err)
		}
		if days := ops[0].ScheduledEnd.Sub(ops[0].ScheduledStart).Hours() / 24; days != 3 {
			t.Fatalf("span=%v days want 3", days)
		}
		// Even spread (334/333/333) never exceeds 480 on an empty calendar.
		if ops[0].Overloaded {
			t.Fatal("lone spanning op should not overload")
		}
		// Prior load on an occupied day pushes it over: flagged, not moved.
		loaded, err := ScheduleOperations(big, 10, start, caps, DayLoad{10: {"2026-09-22": 200}})
		if err != nil {
			t.Fatalf("schedule: %v", err)
		}
		if !loaded[0].Overloaded {
			t.Fatal("occupied-day overload should flag")
		}
		if !loaded[0].ScheduledStart.Equal(start) {
			t.Fatalf("start=%v want %v (no force-resolve)", loaded[0].ScheduledStart, start)
		}
	})

	t.Run("prior load overload flagged not resolved", func(t *testing.T) {
		prior := DayLoad{10: {"2026-09-21": 450}}
		ops, err := ScheduleOperations(routing, 10, start, caps, prior) // op1 needs 80, 30 free
		if err != nil {
			t.Fatalf("schedule: %v", err)
		}
		if !ops[0].Overloaded || ops[1].Overloaded {
			t.Fatalf("only op1 should overload: %+v", ops)
		}
		// Forward placement kept: still starts on the requested day.
		if !ops[0].ScheduledStart.Equal(start) {
			t.Fatalf("start=%v want %v (no force-resolve)", ops[0].ScheduledStart, start)
		}
	})

	t.Run("same-day accumulation overloads second op", func(t *testing.T) {
		two := []BOMOperation{
			{EntityID: 1, BOMID: 7, Seq: 1, WorkstationID: 10, RunMinutesPerUnit: 40},
			{EntityID: 1, BOMID: 7, Seq: 2, WorkstationID: 10, RunMinutesPerUnit: 10},
		}
		// qty 10: op1 400min day1 (chained, so op2 lands day2 — no overload).
		ops, err := ScheduleOperations(two, 10, start, map[int64]int64{10: 480}, nil)
		if err != nil {
			t.Fatalf("schedule: %v", err)
		}
		if ops[0].Overloaded || ops[1].Overloaded {
			t.Fatalf("chained days should not overload: %+v", ops)
		}
		// Same-day collision via prior load on op2's day instead.
		prior := DayLoad{10: {"2026-09-22": 400}}
		ops, err = ScheduleOperations(two, 10, start, map[int64]int64{10: 480}, prior)
		if err != nil {
			t.Fatalf("schedule: %v", err)
		}
		if ops[0].Overloaded || !ops[1].Overloaded {
			t.Fatalf("only op2 should overload: %+v", ops)
		}
	})

	for name, fn := range map[string]func() ([]MOOperation, error){
		"zero qty":      func() ([]MOOperation, error) { return ScheduleOperations(routing, 0, start, caps, nil) },
		"empty routing": func() ([]MOOperation, error) { return ScheduleOperations(nil, 5, start, caps, nil) },
		"unknown ws": func() ([]MOOperation, error) {
			return ScheduleOperations(routing, 5, start, map[int64]int64{10: 480}, nil)
		},
		"zero capacity": func() ([]MOOperation, error) {
			return ScheduleOperations(routing[:1], 5, start, map[int64]int64{10: 0}, nil)
		},
		"zero minutes": func() ([]MOOperation, error) {
			return ScheduleOperations([]BOMOperation{{EntityID: 1, BOMID: 7, Seq: 1, WorkstationID: 10}}, 5, start, caps, nil)
		},
		"duplicate seq": func() ([]MOOperation, error) {
			dup := append([]BOMOperation(nil), routing...)
			dup[1].Seq = 2
			dup = append(dup, BOMOperation{EntityID: 1, BOMID: 7, Seq: 2, WorkstationID: 10, RunMinutesPerUnit: 1})
			return ScheduleOperations(dup, 5, start, caps, nil)
		},
	} {
		if _, err := fn(); !errors.Is(err, platform.ErrValidation) {
			t.Errorf("%s: err=%v want ErrValidation", name, err)
		}
	}
}

func TestCapacityMath(t *testing.T) {
	day := func(s string) time.Time {
		d, _ := time.Parse("2006-01-02", s)
		return d
	}
	ops := []MOOperation{
		{WorkstationID: 10, PlannedMinutes: 500, ScheduledStart: day("2026-09-21"), ScheduledEnd: day("2026-09-23"), Status: MOOpPending},
		{WorkstationID: 10, PlannedMinutes: 60, ScheduledStart: day("2026-09-21"), ScheduledEnd: day("2026-09-22"), Status: MOOpDone},
		{WorkstationID: 10, PlannedMinutes: 999, ScheduledStart: day("2026-09-21"), ScheduledEnd: day("2026-09-22"), Status: MOOpCanceled},
		{WorkstationID: 99, PlannedMinutes: 999, ScheduledStart: day("2026-09-21"), ScheduledEnd: day("2026-09-22"), Status: MOOpPending},
	}
	view := CapacityView(ops, 10, 480, day("2026-09-21"), day("2026-09-23"))
	if len(view) != 3 {
		t.Fatalf("days=%d want 3", len(view))
	}
	// 500 over 2 days spreads 250/250; +60 on day 1 => 310/250/0.
	if view[0].LoadMinutes != 310 || view[1].LoadMinutes != 250 || view[2].LoadMinutes != 0 {
		t.Fatalf("loads=%d/%d/%d want 310/250/0", view[0].LoadMinutes, view[1].LoadMinutes, view[2].LoadMinutes)
	}
	if view[0].Overloaded || view[1].Overloaded {
		t.Fatal("no day exceeds 480")
	}
	over := CapacityView(ops, 10, 300, day("2026-09-21"), day("2026-09-21"))
	if len(over) != 1 || !over[0].Overloaded || over[0].LoadMinutes != 310 {
		t.Fatalf("overload view=%+v", over)
	}
	if got := CapacityView(ops, 10, 0, day("2026-09-21"), day("2026-09-21")); got != nil {
		t.Fatalf("zero capacity should yield nil, got %+v", got)
	}
}

func setupScheduledMO(t *testing.T, ctx context.Context, st *MemoryStore, ledger *catalog.MemoryStore, qty int64) (ManufacturingOrder, []MOOperation) {
	t.Helper()
	seedLedger(t, ctx, ledger)
	ws1 := mustWorkstation(t, ctx, st, "WS-A", 480)
	ws2 := mustWorkstation(t, ctx, st, "WS-B", 480)
	bom := &BOM{EntityID: 1, Ref: "BOM-S", ProductID: 1, Label: "Sched"}
	if err := st.CreateBOM(ctx, nil, bom); err != nil {
		t.Fatalf("bom: %v", err)
	}
	for _, l := range []BOMLine{
		{EntityID: 1, BOMID: bom.ID, ComponentID: 2, Qty: 3},
		{EntityID: 1, BOMID: bom.ID, ComponentID: 3, Qty: 1},
	} {
		l := l
		if err := st.AddLine(ctx, nil, &l); err != nil {
			t.Fatalf("line: %v", err)
		}
	}
	for _, o := range []BOMOperation{
		{EntityID: 1, BOMID: bom.ID, WorkstationID: ws1.ID, RunMinutesPerUnit: 5, SetupMinutes: 10},
		{EntityID: 1, BOMID: bom.ID, WorkstationID: ws2.ID, RunMinutesPerUnit: 8},
	} {
		o := o
		if err := st.AddBOMOperation(ctx, nil, &o); err != nil {
			t.Fatalf("routing: %v", err)
		}
	}
	mo := &ManufacturingOrder{EntityID: 1, Ref: "MO-S", BOMID: bom.ID, ProductID: 1, WarehouseID: 1, Qty: qty}
	if err := st.CreateMO(ctx, nil, mo); err != nil {
		t.Fatalf("mo: %v", err)
	}
	svc := NewService(nil, st, testLedger{ledger}, nil)
	upd, err := st.SetMOStatus(ctx, nil, 1, mo.ID, MOValidated, mo.RowVersion)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	ops, err := svc.Schedule(ctx, ScheduleCmd{EntityID: 1, MOID: mo.ID,
		Start: time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	upd, err = st.SetMOStatus(ctx, nil, 1, mo.ID, MOInProgress, upd.RowVersion)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	*mo = upd
	return *mo, ops
}

func TestOperationCompletionMemory(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	ledger := catalog.NewMemoryStore()
	mo, ops := setupScheduledMO(t, ctx, st, ledger, 10)
	if len(ops) != 2 || ops[0].PlannedMinutes != 60 || ops[1].PlannedMinutes != 80 {
		t.Fatalf("scheduled=%+v want 60/80", ops)
	}
	svc := NewService(nil, st, testLedger{ledger}, nil)

	// Out-of-order completion refused.
	if _, err := svc.CompleteOperation(ctx, CompleteOperationCmd{EntityID: 1, MOID: mo.ID, Seq: 2, ActualMinutes: 5}); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("out-of-order err=%v want ErrValidation", err)
	}
	// Direct produce refused once a schedule exists.
	if _, _, err := svc.Produce(ctx, ProduceCmd{EntityID: 1, MOID: mo.ID}); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("direct produce err=%v want ErrValidation", err)
	}

	// Step 1 (intermediate): posts floor shares (30/2=15 of comp2, 10/2=5 of comp3).
	res, err := svc.CompleteOperation(ctx, CompleteOperationCmd{EntityID: 1, MOID: mo.ID, Seq: 1, ActualMinutes: 55})
	if err != nil {
		t.Fatalf("complete op1: %v", err)
	}
	if res.Produced || res.Operation.Status != MOOpDone || res.Operation.ActualMinutes != 55 {
		t.Fatalf("op1 result=%+v", res)
	}
	lvl2, _ := ledger.Level(ctx, nil, 2, 1)
	lvl3, _ := ledger.Level(ctx, nil, 3, 1)
	if lvl2.Qty != 85 || lvl3.Qty != 45 {
		t.Fatalf("after op1 levels=%d/%d want 85/45", lvl2.Qty, lvl3.Qty)
	}
	still, _ := st.MOByID(ctx, nil, 1, mo.ID)
	if still.Status != MOInProgress {
		t.Fatalf("MO status=%d want in-progress after intermediate", still.Status)
	}

	// Double completion refused.
	if _, err := svc.CompleteOperation(ctx, CompleteOperationCmd{EntityID: 1, MOID: mo.ID, Seq: 1}); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("double complete err=%v want ErrValidation", err)
	}

	// Final step: remainder consumes (15/5) + receipt, MO produced.
	res, err = svc.CompleteOperation(ctx, CompleteOperationCmd{EntityID: 1, MOID: mo.ID, Seq: 2, ActualMinutes: 90})
	if err != nil {
		t.Fatalf("complete op2: %v", err)
	}
	if !res.Produced || res.MO.Status != MOProduced {
		t.Fatalf("final result=%+v", res)
	}
	lvl2, _ = ledger.Level(ctx, nil, 2, 1)
	lvl3, _ = ledger.Level(ctx, nil, 3, 1)
	lvl1, _ := ledger.Level(ctx, nil, 1, 1)
	if lvl2.Qty != 70 || lvl3.Qty != 40 || lvl1.Qty != 10 {
		t.Fatalf("final levels=%d/%d/%d want 70/40/10", lvl2.Qty, lvl3.Qty, lvl1.Qty)
	}
	// Negative actual minutes refused (on a fresh MO).
	mo2 := &ManufacturingOrder{EntityID: 1, Ref: "MO-NEG", BOMID: mo.BOMID, ProductID: 1, WarehouseID: 1, Qty: 1}
	if err := st.CreateMO(ctx, nil, mo2); err != nil {
		t.Fatalf("mo2: %v", err)
	}
	upd, _ := st.SetMOStatus(ctx, nil, 1, mo2.ID, MOValidated, mo2.RowVersion)
	if _, err := svc.Schedule(ctx, ScheduleCmd{EntityID: 1, MOID: mo2.ID}); err != nil {
		t.Fatalf("schedule mo2: %v", err)
	}
	upd, _ = st.SetMOStatus(ctx, nil, 1, mo2.ID, MOInProgress, upd.RowVersion)
	_ = upd
	if _, err := svc.CompleteOperation(ctx, CompleteOperationCmd{EntityID: 1, MOID: mo2.ID, Seq: 1, ActualMinutes: -1}); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("negative actual err=%v want ErrValidation", err)
	}
}

func TestWorkstationRoutingMemoryGuards(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	mustWorkstation(t, ctx, st, "WS-1", 480)
	dup := &Workstation{EntityID: 1, Code: "WS-1", Label: "dup", DailyCapacityMin: 60, Status: WorkstationActive}
	if err := st.CreateWorkstation(ctx, nil, dup); !errors.Is(err, platform.ErrConflict) {
		t.Fatalf("duplicate code err=%v want ErrConflict", err)
	}
	bad := &Workstation{EntityID: 1, Code: "WS-0", Label: "zero", DailyCapacityMin: 0}
	if err := st.CreateWorkstation(ctx, nil, bad); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("zero capacity err=%v want ErrValidation", err)
	}
	if _, err := st.WorkstationByID(ctx, nil, 2, 1); !errors.Is(err, identityErrNotFound()) {
		t.Fatalf("cross-tenant ws err=%v want not-found", err)
	}
	bom := &BOM{EntityID: 1, Ref: "BOM-G", ProductID: 1, Label: "G"}
	if err := st.CreateBOM(ctx, nil, bom); err != nil {
		t.Fatalf("bom: %v", err)
	}
	ghost := &BOMOperation{EntityID: 1, BOMID: bom.ID, WorkstationID: 999, RunMinutesPerUnit: 1}
	if err := st.AddBOMOperation(ctx, nil, ghost); err == nil {
		t.Fatal("unknown workstation accepted")
	}
	// Scheduling with no routing refused.
	mo := &ManufacturingOrder{EntityID: 1, Ref: "MO-G", BOMID: bom.ID, ProductID: 1, WarehouseID: 1, Qty: 1}
	if err := st.CreateMO(ctx, nil, mo); err != nil {
		t.Fatalf("mo: %v", err)
	}
	svc := NewService(nil, st, testLedger{catalog.NewMemoryStore()}, nil)
	if _, err := svc.Schedule(ctx, ScheduleCmd{EntityID: 1, MOID: mo.ID}); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("empty routing schedule err=%v want ErrValidation", err)
	}
}

func identityErrNotFound() error {
	// identity.ErrNotFound aliases platform.ErrNotFound; errors.Is covers both.
	return platform.ErrNotFound
}

func TestWorkstationHTTPFlow(t *testing.T) {
	h, ledger := testRouter()
	ctx := t.Context()
	for _, mv := range []catalog.StockMovement{
		{EntityID: 1, ProductID: 2, WarehouseID: 1, Qty: 100, Reason: catalog.ReasonReceipt, Ref: "OPEN"},
	} {
		m := mv
		if _, err := ledger.AppendMovement(ctx, nil, &m, false); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	rec := doReq(t, h, http.MethodPost, "/api/v1/manufacturing/workstations",
		map[string]any{"code": "WS-HTTP", "label": "HTTP cell", "daily_capacity_min": 480})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create WS: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodPost, "/api/v1/manufacturing/boms",
		map[string]any{"ref": "BOM-H", "product_id": 1, "label": "H"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create BOM: code=%d", rec.Code)
	}
	var bom BOM
	jsonDecode(rec, &bom)
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/manufacturing/boms/%d/lines", bom.ID),
		map[string]any{"component_id": 2, "qty": 2})
	if rec.Code != http.StatusCreated {
		t.Fatalf("add line: code=%d", rec.Code)
	}
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/manufacturing/boms/%d/operations", bom.ID),
		map[string]any{"workstation_id": 1, "run_minutes_per_unit": 10, "setup_minutes": 5})
	if rec.Code != http.StatusCreated {
		t.Fatalf("add routing: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, fmt.Sprintf("/api/v1/manufacturing/boms/%d/operations", bom.ID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list routing: code=%d", rec.Code)
	}
	rec = doReq(t, h, http.MethodPost, "/api/v1/manufacturing/mos",
		map[string]any{"ref": "MO-H", "bom_id": bom.ID, "product_id": 1, "warehouse_id": 1, "qty": 4})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create MO: code=%d", rec.Code)
	}
	var mo ManufacturingOrder
	jsonDecode(rec, &mo)
	for _, st := range []int16{1, 2} {
		rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/manufacturing/mos/%d/status", mo.ID),
			map[string]any{"status": st, "row_version": mo.RowVersion})
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: code=%d body=%s", st, rec.Code, rec.Body.String())
		}
		jsonDecode(rec, &mo)
	}
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/manufacturing/mos/%d/schedule", mo.ID),
		map[string]any{"start": "2026-09-21"})
	if rec.Code != http.StatusOK {
		t.Fatalf("schedule: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet,
		"/api/v1/manufacturing/capacity?workstation_id=1&from=2026-09-21&to=2026-09-22", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("capacity: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var days []CapacityDay
	jsonDecode(rec, &days)
	if len(days) != 2 || days[0].LoadMinutes != 45 {
		t.Fatalf("capacity=%+v want 45min on day 1", days)
	}
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/manufacturing/mos/%d/operations/1/complete", mo.ID),
		map[string]any{"actual_minutes": 50})
	if rec.Code != http.StatusOK {
		t.Fatalf("complete: code=%d body=%s", rec.Code, rec.Body.String())
	}
	lvl2, _ := ledger.Level(ctx, nil, 2, 1)
	lvl1, _ := ledger.Level(ctx, nil, 1, 1)
	if lvl2.Qty != 92 || lvl1.Qty != 4 {
		t.Fatalf("levels=%d/%d want 92/4", lvl2.Qty, lvl1.Qty)
	}
	// Single-op MO: completion produced it, so direct produce is 422.
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/manufacturing/mos/%d/produce", mo.ID), nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("produce after schedule: code=%d want 422", rec.Code)
	}
}

func TestPGWorkstationRoutingProduce(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	cst := catalog.NewPGStore(pool)
	p := &catalog.Product{EntityID: 1, SKU: "PG-MRP-FG", Name: "FG", Type: catalog.ProductGoods, Status: catalog.ProductActive}
	if err := cst.CreateProduct(ctx, pool, p); err != nil {
		t.Fatalf("product: %v", err)
	}
	c := &catalog.Product{EntityID: 1, SKU: "PG-MRP-CMP", Name: "Comp", Type: catalog.ProductGoods, Status: catalog.ProductActive}
	if err := cst.CreateProduct(ctx, pool, c); err != nil {
		t.Fatalf("component: %v", err)
	}
	w := &catalog.Warehouse{EntityID: 1, Code: "PGMRPW", Label: "W", Status: 1}
	if err := cst.CreateWarehouse(ctx, pool, w); err != nil {
		t.Fatalf("warehouse: %v", err)
	}
	mv := &catalog.StockMovement{EntityID: 1, ProductID: c.ID, WarehouseID: w.ID, Qty: 100, Reason: catalog.ReasonReceipt, Ref: "OPEN"}
	if _, err := cst.AppendMovement(ctx, pool, mv, false); err != nil {
		t.Fatalf("seed: %v", err)
	}

	st := NewPGStore(pool)
	svc := NewService(pool, st, cst, nil) // Pool set: TxEntity paths.
	ws := &Workstation{EntityID: 1, Code: "PG-WS", Label: "PG cell", DailyCapacityMin: 480, Status: WorkstationActive}
	if err := st.CreateWorkstation(ctx, pool, ws); err != nil {
		t.Fatalf("workstation: %v", err)
	}
	got, err := st.WorkstationByID(ctx, pool, 1, ws.ID)
	if err != nil || got.Code != "PG-WS" {
		t.Fatalf("workstation read: %+v err=%v", got, err)
	}
	bom := &BOM{EntityID: 1, Ref: "PG-MRP-BOM", ProductID: p.ID, Label: "B"}
	if err := st.CreateBOM(ctx, pool, bom); err != nil {
		t.Fatalf("bom: %v", err)
	}
	if err := st.AddLine(ctx, pool, &BOMLine{EntityID: 1, BOMID: bom.ID, ComponentID: c.ID, Qty: 2}); err != nil {
		t.Fatalf("line: %v", err)
	}
	op := &BOMOperation{EntityID: 1, BOMID: bom.ID, WorkstationID: ws.ID, RunMinutesPerUnit: 10, SetupMinutes: 5}
	if err := st.AddBOMOperation(ctx, pool, op); err != nil {
		t.Fatalf("routing: %v", err)
	}
	if op.Seq != 1 {
		t.Fatalf("auto seq=%d want 1", op.Seq)
	}
	routing, err := st.BOMOperations(ctx, pool, bom.ID)
	if err != nil || len(routing) != 1 {
		t.Fatalf("routing read: %+v err=%v", routing, err)
	}

	mo := &ManufacturingOrder{EntityID: 1, Ref: "PG-MRP-MO", BOMID: bom.ID, ProductID: p.ID, WarehouseID: w.ID, Qty: 5}
	if err := st.CreateMO(ctx, pool, mo); err != nil {
		t.Fatalf("mo: %v", err)
	}
	upd, err := st.SetMOStatus(ctx, pool, 1, mo.ID, MOValidated, mo.RowVersion)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	sched, err := svc.Schedule(ctx, ScheduleCmd{EntityID: 1, MOID: mo.ID,
		Start: time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if len(sched) != 1 || sched[0].PlannedMinutes != 55 {
		t.Fatalf("scheduled=%+v want 55min", sched)
	}
	upd, err = st.SetMOStatus(ctx, pool, 1, mo.ID, MOInProgress, upd.RowVersion)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	res, err := svc.CompleteOperation(ctx, CompleteOperationCmd{EntityID: 1, MOID: mo.ID, Seq: 1, ActualMinutes: 60})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if !res.Produced || res.MO.Status != MOProduced {
		t.Fatalf("result=%+v", res)
	}
	lvlC, _ := cst.Level(ctx, pool, c.ID, w.ID)
	lvlP, _ := cst.Level(ctx, pool, p.ID, w.ID)
	if lvlC.Qty != 90 || lvlP.Qty != 5 {
		t.Fatalf("levels=%d/%d want 90/5", lvlC.Qty, lvlP.Qty)
	}
	_ = upd
}
