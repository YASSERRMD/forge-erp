package partnership

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
	"github.com/go-chi/chi/v5"
)

func passthrough(_, _, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(platform.ContextWithEntity(r.Context(), 1)))
		})
	}
}

func TestTierMath(t *testing.T) {
	tiers := []Tier{
		{Name: "bronze", MinTotal: 0, RateBps: 100},
		{Name: "silver", MinTotal: 100000, RateBps: 250},
		{Name: "gold", MinTotal: 500000, RateBps: 500},
	}
	for total, want := range map[int64]int64{0: 100, 99999: 100, 100000: 250, 499999: 250, 500000: 500, 9000000: 500} {
		if got := TierRate(tiers, total); got != want {
			t.Errorf("TierRate(%d)=%d want %d", total, got, want)
		}
	}
	if got := TierRate(nil, 1000); got != 0 {
		t.Errorf("TierRate(nil)=%d want 0", got)
	}
	// bps math stays in int64 minor units.
	amt, err := AccrueAmount(19999, 250)
	if err != nil || amt != 499 {
		t.Fatalf("AccrueAmount(19999,250)=%d,%v want 499", amt, err)
	}
	if _, err := AccrueAmount(0, 100); err == nil {
		t.Error("zero sale_total accepted")
	}
	if _, err := AccrueAmount(100, 10001); err == nil {
		t.Error("rate_bps > 10000 accepted")
	}
	bad := Program{EntityID: 1, Code: "P", Name: "P",
		Tiers: []Tier{{Name: "b", MinTotal: 100, RateBps: 50}, {Name: "a", MinTotal: 50, RateBps: 60}}}
	if err := bad.Validate(); err == nil {
		t.Error("non-ascending tiers accepted")
	}
}

func TestReferralAndAccrualMemory(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	svc := &Service{Store: m}
	p := &Program{EntityID: 1, Code: "REF", Name: "Referral",
		Tiers: []Tier{{Name: "base", MinTotal: 0, RateBps: 100}, {Name: "vip", MinTotal: 100000, RateBps: 500}}}
	if err := m.CreateProgram(ctx, nil, p); err != nil {
		t.Fatalf("program: %v", err)
	}
	dup := &Program{EntityID: 1, Code: "REF", Name: "Dup"}
	if err := m.CreateProgram(ctx, nil, dup); err == nil {
		t.Error("duplicate program code accepted")
	}
	ref, err := svc.RegisterReferral(ctx, 1, p.ID, 10, 20, "LINK-1")
	if err != nil {
		t.Fatalf("referral: %v", err)
	}
	if _, err := svc.RegisterReferral(ctx, 1, p.ID, 10, 10, "SELF"); err == nil {
		t.Error("self-referral accepted")
	}
	// First sale 90000 → cumulative 90000 → base 100bps → 900.
	a1, err := svc.AccrueCommission(ctx, 1, ref.ID, 90000)
	if err != nil {
		t.Fatalf("accrue1: %v", err)
	}
	if a1.RateBps != 100 || a1.Amount != 900 {
		t.Fatalf("accrue1=%+v want rate 100 amount 900", a1)
	}
	// Second sale 20000 → cumulative 110000 → vip 500bps → 1000.
	a2, err := svc.AccrueCommission(ctx, 1, ref.ID, 20000)
	if err != nil {
		t.Fatalf("accrue2: %v", err)
	}
	if a2.RateBps != 500 || a2.Amount != 1000 {
		t.Fatalf("accrue2=%+v want rate 500 amount 1000", a2)
	}
	total, _ := m.ReferredTotal(ctx, nil, 1, ref.ID)
	if total != 110000 {
		t.Fatalf("total=%d want 110000", total)
	}
	if _, err := svc.AccrueCommission(ctx, 2, ref.ID, 100); err == nil {
		t.Error("cross-tenant accrue accepted")
	}
}

func TestPartnershipAPI(t *testing.T) {
	st := NewMemoryStore()
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Store: st}, passthrough)
	})
	post := func(path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	rec := post("/api/v1/partner-programs", map[string]any{
		"code": "REF", "name": "Referral",
		"tiers": []map[string]any{{"name": "base", "min_total": 0, "rate_bps": 100}},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("program: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var p Program
	_ = json.NewDecoder(rec.Body).Decode(&p)
	rec = post(fmt.Sprintf("/api/v1/partner-programs/%d/referrals", p.ID),
		map[string]any{"referrer_org_id": 10, "referred_org_id": 20, "code": "LINK-1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("referral: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var ref Referral
	_ = json.NewDecoder(rec.Body).Decode(&ref)
	rec = post(fmt.Sprintf("/api/v1/referrals/%d/accruals", ref.ID), map[string]any{"sale_total": 50000})
	if rec.Code != http.StatusCreated {
		t.Fatalf("accrue: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var a Accrual
	_ = json.NewDecoder(rec.Body).Decode(&a)
	if a.Amount != 500 {
		t.Fatalf("amount=%d want 500", a.Amount)
	}
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/referrals/%d/accruals", ref.ID), nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var list []Accrual
	_ = json.NewDecoder(rec.Body).Decode(&list)
	if len(list) != 1 {
		t.Fatalf("accruals=%d want 1", len(list))
	}
}

func TestPGPartnershipFlow(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	st := NewPGStore(pool)
	svc := &Service{Store: st, DB: pool}
	p := &Program{EntityID: 1, Code: "REF", Name: "Referral",
		Tiers: []Tier{{Name: "base", MinTotal: 0, RateBps: 200}}}
	if err := st.CreateProgram(ctx, pool, p); err != nil {
		t.Fatalf("program: %v", err)
	}
	ref, err := svc.RegisterReferral(ctx, 1, p.ID, 10, 20, "LINK-PG")
	if err != nil {
		t.Fatalf("referral: %v", err)
	}
	got, err := st.ReferralByCode(ctx, pool, 1, "LINK-PG")
	if err != nil || got.ID != ref.ID {
		t.Fatalf("by code: %+v err=%v", got, err)
	}
	a, err := svc.AccrueCommission(ctx, 1, ref.ID, 10000)
	if err != nil {
		t.Fatalf("accrue: %v", err)
	}
	if a.Amount != 200 {
		t.Fatalf("amount=%d want 200", a.Amount)
	}
	list, err := st.AccrualsOf(ctx, pool, 1, ref.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("accruals=%+v err=%v", list, err)
	}
	if _, err := st.ProgramByID(ctx, pool, 2, p.ID); err == nil {
		t.Error("cross-tenant ProgramByID accepted")
	}
}
