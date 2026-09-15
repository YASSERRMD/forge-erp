package assets

import (
	"context"
	"testing"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/finance"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

func planAmounts(plan []DepLine) []int64 {
	out := make([]int64, len(plan))
	for i, l := range plan {
		out[i] = l.Amount
	}
	return out
}

func planSum(plan []DepLine) int64 {
	var s int64
	for _, l := range plan {
		s += l.Amount
	}
	return s
}

func eqSlice(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestBuildScheduleVectors(t *testing.T) {
	// Linear exact split.
	p, err := BuildSchedule(1200, DepLinear, 0, 4)
	if err != nil || !eqSlice(planAmounts(p), []int64{300, 300, 300, 300}) {
		t.Fatalf("linear exact: %v %v", planAmounts(p), err)
	}
	// Linear remainder absorbed by the last period; sums to cost.
	p, err = BuildSchedule(1000, DepLinear, 0, 3)
	if err != nil || !eqSlice(planAmounts(p), []int64{333, 333, 334}) {
		t.Fatalf("linear remainder: %v %v", planAmounts(p), err)
	}
	// Degressive declining balance: 10000 @20%/period over 3.
	p, err = BuildSchedule(10000, DepDegressive, 2000, 3)
	if err != nil || !eqSlice(planAmounts(p), []int64{2000, 1600, 6400}) {
		t.Fatalf("degressive: %v %v", planAmounts(p), err)
	}
	// Degressive rounding edge: 1001 @10%/period over 2 —
	// P1 = half-up(1001*1000/10000) = half-up(100.1) = 100, P2 = 901.
	p, err = BuildSchedule(1001, DepDegressive, 1000, 2)
	if err != nil || !eqSlice(planAmounts(p), []int64{100, 901}) {
		t.Fatalf("rounding edge: %v %v", planAmounts(p), err)
	}
	// Degressive half-up rounds .5 up: remaining 1 @50% over 2 → P1 = 1, P2 = 0.
	p, err = BuildSchedule(1, DepDegressive, 5000, 2)
	if err != nil || !eqSlice(planAmounts(p), []int64{1, 0}) {
		t.Fatalf("half-up edge: %v %v", planAmounts(p), err)
	}
	// Every vector sums to cost.
	for _, tc := range [][4]int64{{1200, 0, 4, 0}, {1000, 0, 3, 0}, {10000, 2000, 3, 1}, {1001, 1000, 2, 1}} {
		method := DepLinear
		if tc[3] == 1 {
			method = DepDegressive
		}
		pp, err := BuildSchedule(tc[0], method, tc[1], int(tc[2]))
		if err != nil || planSum(pp) != tc[0] {
			t.Fatalf("sum %v: %v %v", tc, planAmounts(pp), err)
		}
	}
	// Rejections.
	if _, err := BuildSchedule(100, "sum-of-years", 0, 4); err == nil {
		t.Error("unknown method accepted")
	}
	if _, err := BuildSchedule(100, DepDegressive, 0, 4); err == nil {
		t.Error("zero degressive rate accepted")
	}
	if _, err := BuildSchedule(0, DepLinear, 0, 4); err == nil {
		t.Error("zero cost accepted")
	}
	if _, err := BuildSchedule(100, DepLinear, 0, 0); err == nil {
		t.Error("zero periods accepted")
	}
}

func depTestSetup(t *testing.T, ctx context.Context, fstore *finance.MemoryStore) (int64, int64, int64, int64, int64, int64, int64) {
	t.Helper()
	for _, a := range []finance.Account{
		{EntityID: 1, Code: "681000", Label: "Depreciation", Type: "expense"},
		{EntityID: 1, Code: "281000", Label: "Accum. depreciation", Type: "asset"},
		{EntityID: 1, Code: "512000", Label: "Bank", Type: "asset"},
		{EntityID: 1, Code: "215000", Label: "Equipment cost", Type: "asset"},
		{EntityID: 1, Code: "775000", Label: "Disposal gains", Type: "revenue"},
		{EntityID: 1, Code: "675000", Label: "Disposal losses", Type: "expense"},
	} {
		a := a
		if err := fstore.CreateAccount(ctx, nil, &a); err != nil {
			t.Fatalf("account: %v", err)
		}
	}
	j := &finance.Journal{EntityID: 1, Code: "OD", Label: "Operations"}
	if err := fstore.CreateJournal(ctx, nil, j); err != nil {
		t.Fatalf("journal: %v", err)
	}
	accts, _ := fstore.Accounts(ctx, nil, 1)
	byCode := map[string]int64{}
	for _, a := range accts {
		byCode[a.Code] = a.ID
	}
	return j.ID, byCode["681000"], byCode["281000"], byCode["512000"],
		byCode["215000"], byCode["775000"], byCode["675000"]
}

func TestDepreciationPostAndDisposalGain(t *testing.T) {
	ctx := context.Background()
	mstore := NewMemoryStore()
	fstore := finance.NewMemoryStore()
	jid, depExp, accum, bank, costAcct, gain, loss := depTestSetup(t, ctx, fstore)

	a := &Asset{EntityID: 1, Code: "PRESS-1", Label: "Press", Kind: "equipment", Status: AssetInService}
	if err := mstore.CreateAsset(ctx, nil, a); err != nil {
		t.Fatalf("asset: %v", err)
	}
	sc := &AssetSchedule{EntityID: 1, AssetID: a.ID, Method: DepLinear, Cost: 10000,
		StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Periods: 5}
	if err := mstore.CreateSchedule(ctx, nil, sc); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	svc := NewDepService(nil, mstore, fstore, nil)
	for i, want := range []int64{2000, 2000} {
		res, err := svc.Post(ctx, PostDepCmd{EntityID: 1, ScheduleID: sc.ID, JournalID: jid,
			ExpenseAccount: depExp, AccumAccount: accum, RowVersion: sc.RowVersion})
		if err != nil {
			t.Fatalf("post %d: %v", i, err)
		}
		if res.Amount != want || res.Seq != i+1 {
			t.Fatalf("post %d: seq=%d amount=%d", i, res.Seq, res.Amount)
		}
		sc = &res.Schedule
	}
	if sc.Accumulated != 4000 || sc.PostedPeriods != 2 {
		t.Fatalf("schedule=%+v want accum 4000 periods 2", sc)
	}
	tb, _ := fstore.TrialBalance(ctx, nil, 1)
	if tb[depExp] != [2]int64{4000, 0} || tb[accum] != [2]int64{0, 4000} {
		t.Fatalf("depreciation trial=%v", tb)
	}
	// NBV = 10000-4000 = 6000; proceeds 7000 → gain 1000.
	ret, err := svc.Dispose(ctx, DisposeCmd{EntityID: 1, AssetID: a.ID, JournalID: jid,
		CashAccount: bank, CostAccount: costAcct, AccumAccount: accum,
		GainAccount: gain, LossAccount: loss, Proceeds: 7000, RowVersion: a.RowVersion})
	if err != nil {
		t.Fatalf("dispose: %v", err)
	}
	if ret.Status != AssetRetired {
		t.Fatalf("status=%d want retired", ret.Status)
	}
	tb, _ = fstore.TrialBalance(ctx, nil, 1)
	if tb[bank] != [2]int64{7000, 0} {
		t.Fatalf("cash=%v want debit 7000", tb[bank])
	}
	if tb[costAcct] != [2]int64{0, 10000} {
		t.Fatalf("cost=%v want credit 10000", tb[costAcct])
	}
	if tb[gain] != [2]int64{0, 1000} {
		t.Fatalf("gain=%v want credit 1000", tb[gain])
	}
	if tb[accum] != [2]int64{4000, 4000} {
		t.Fatalf("accum=%v want 4000/4000", tb[accum])
	}
	// Balance check: total debits == total credits across all accounts.
	var dr, cr int64
	for _, v := range tb {
		dr += v[0]
		cr += v[1]
	}
	if dr != cr {
		t.Fatalf("unbalanced: debit %d != credit %d", dr, cr)
	}
}

func TestDisposalLoss(t *testing.T) {
	ctx := context.Background()
	mstore := NewMemoryStore()
	fstore := finance.NewMemoryStore()
	jid, depExp, accum, bank, costAcct, gain, loss := depTestSetup(t, ctx, fstore)

	a := &Asset{EntityID: 1, Code: "VAN-1", Label: "Van", Kind: "vehicle", Status: AssetInService}
	if err := mstore.CreateAsset(ctx, nil, a); err != nil {
		t.Fatalf("asset: %v", err)
	}
	sc := &AssetSchedule{EntityID: 1, AssetID: a.ID, Method: DepDegressive, Cost: 10000,
		RateBps: 2000, StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Periods: 3}
	if err := mstore.CreateSchedule(ctx, nil, sc); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	svc := NewDepService(nil, mstore, fstore, nil)
	res, err := svc.Post(ctx, PostDepCmd{EntityID: 1, ScheduleID: sc.ID, JournalID: jid,
		ExpenseAccount: depExp, AccumAccount: accum, RowVersion: sc.RowVersion})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if res.Amount != 2000 {
		t.Fatalf("degressive P1=%d want 2000", res.Amount)
	}
	// NBV = 8000; proceeds 5000 → loss 3000.
	if _, err := svc.Dispose(ctx, DisposeCmd{EntityID: 1, AssetID: a.ID, JournalID: jid,
		CashAccount: bank, CostAccount: costAcct, AccumAccount: accum,
		GainAccount: gain, LossAccount: loss, Proceeds: 5000, RowVersion: a.RowVersion}); err != nil {
		t.Fatalf("dispose: %v", err)
	}
	tb, _ := fstore.TrialBalance(ctx, nil, 1)
	if tb[loss] != [2]int64{3000, 0} {
		t.Fatalf("loss=%v want debit 3000", tb[loss])
	}
	if tb[gain] != [2]int64{0, 0} {
		t.Fatalf("gain=%v want empty", tb[gain])
	}
	var dr, cr int64
	for _, v := range tb {
		dr += v[0]
		cr += v[1]
	}
	if dr != cr {
		t.Fatalf("unbalanced: debit %d != credit %d", dr, cr)
	}
}

func TestPGAssetSchedulePersist(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	st := NewPGStore(pool)
	a := &Asset{EntityID: 1, Code: "PG-DEP", Label: "PG press", Kind: "equipment", Status: AssetInService}
	if err := st.CreateAsset(ctx, pool, a); err != nil {
		t.Fatalf("asset: %v", err)
	}
	sc := &AssetSchedule{EntityID: 1, AssetID: a.ID, Method: DepLinear, Cost: 9000,
		StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Periods: 3}
	if err := st.CreateSchedule(ctx, pool, sc); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	// Second schedule for the same asset is rejected (one schedule per asset).
	dup := &AssetSchedule{EntityID: 1, AssetID: a.ID, Method: DepLinear, Cost: 9000,
		StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Periods: 3}
	if err := st.CreateSchedule(ctx, pool, dup); err == nil {
		t.Fatal("duplicate schedule accepted")
	}
	plan, err := BuildSchedule(sc.Cost, sc.Method, sc.RateBps, sc.Periods)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	upd, err := st.MarkSchedulePosted(ctx, pool, 1, sc.ID, plan[0].Amount, sc.RowVersion)
	if err != nil {
		t.Fatalf("mark posted: %v", err)
	}
	if upd.PostedPeriods != 1 || upd.Accumulated != 3000 {
		t.Fatalf("upd=%+v want 1 period / 3000", upd)
	}
	got, err := st.ScheduleByID(ctx, pool, 1, sc.ID)
	if err != nil || got.PostedPeriods != 1 || got.Accumulated != 3000 {
		t.Fatalf("persisted=%+v %v", got, err)
	}
	// NOTE: periodic posting + disposal through finance.PostEntry are covered
	// by TestDepreciationPostAndDisposalGain / TestDisposalLoss on memory
	// stores; the pooled service path reuses the payout TxEntity pattern.
}
