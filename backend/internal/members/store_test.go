package members

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
	"github.com/go-chi/chi/v5"
)

func passthrough(_, _, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

func TestMemberSubscriptionFlow(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	ty := &MemberType{EntityID: 1, Code: "STD", Label: "Standard", AnnualFee: 5000}
	if err := m.CreateType(ctx, ty); err != nil {
		t.Fatalf("type: %v", err)
	}
	mb := &Member{EntityID: 1, Ref: "M-1", TypeID: ty.ID, FirstName: "Ada", LastName: "L"}
	if err := m.CreateMember(ctx, mb); err != nil {
		t.Fatalf("member: %v", err)
	}
	if err := m.CreateMember(ctx, &Member{EntityID: 1, Ref: "M-2", TypeID: ty.ID}); err == nil {
		t.Error("nameless member accepted")
	}
	upd, err := m.SetMemberStatus(ctx, 1, mb.ID, MemberActive, mb.RowVersion)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	su := &Subscription{EntityID: 1, MemberID: mb.ID, Year: "2026", Amount: 5000}
	if err := m.CreateSubscription(ctx, su); err != nil {
		t.Fatalf("subscription: %v", err)
	}
	if err := m.CreateSubscription(ctx, &Subscription{EntityID: 1, MemberID: mb.ID, Year: "2026", Amount: 1}); err == nil {
		t.Error("duplicate year accepted")
	}
	su2, err := m.SetSubscriptionStatus(ctx, 1, su.ID, SubValidated, su.RowVersion)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if _, err := m.SetSubscriptionStatus(ctx, 1, su.ID, SubPaid, su2.RowVersion); err != nil {
		t.Fatalf("pay: %v", err)
	}
	if _, err := m.SetMemberStatus(ctx, 1, mb.ID, MemberResigned, upd.RowVersion); err != nil {
		t.Fatalf("resign: %v", err)
	}
}

func TestDonationFlow(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	now := time.Now().UTC().Truncate(time.Second)
	d := &Donation{EntityID: 1, Ref: "DON-1", DonorName: "Grace Hopper",
		Amount: 10000, DonatedAt: now, Method: "transfer"}
	if err := m.CreateDonation(ctx, d); err != nil {
		t.Fatalf("donation: %v", err)
	}
	if err := m.CreateDonation(ctx, &Donation{EntityID: 1, Ref: "DON-2", DonorName: "x",
		Amount: 1, DonatedAt: now, Method: "crypto"}); err == nil {
		t.Error("bad method accepted")
	}
	upd, err := m.SetDonationStatus(ctx, 1, d.ID, DonationPaid, d.RowVersion)
	if err != nil {
		t.Fatalf("pay: %v", err)
	}
	if _, err := m.SetDonationStatus(ctx, 1, d.ID, DonationCanceled, upd.RowVersion); err == nil {
		t.Error("paid→canceled accepted")
	}
}

func TestMembersAPI(t *testing.T) {
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
	rec := post("/api/v1/member-types", map[string]any{"code": "STD", "label": "Std", "annual_fee": 100})
	if rec.Code != http.StatusCreated {
		t.Fatalf("type: code=%d", rec.Code)
	}
	var ty MemberType
	_ = json.NewDecoder(rec.Body).Decode(&ty)
	rec = post("/api/v1/members", map[string]any{"ref": "M-9", "type_id": ty.ID,
		"first_name": "Alan", "last_name": "Turing"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("member: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var mb Member
	_ = json.NewDecoder(rec.Body).Decode(&mb)
	rec = post("/api/v1/members/9999/status", map[string]any{"status": 1, "row_version": 1})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing: code=%d want 404", rec.Code)
	}
}

func TestCrossTenantIsolation(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	ty := &MemberType{EntityID: 1, Code: "X", Label: "X", AnnualFee: 100}
	if err := m.CreateType(ctx, ty); err != nil {
		t.Fatalf("type: %v", err)
	}
	mb := &Member{EntityID: 1, Ref: "X-1", TypeID: ty.ID, FirstName: "Ada", LastName: "L"}
	if err := m.CreateMember(ctx, mb); err != nil {
		t.Fatalf("member: %v", err)
	}
	if _, err := m.MemberByID(ctx, 2, mb.ID); err == nil {
		t.Error("cross-tenant MemberByID accepted")
	}
	if _, err := m.SetMemberStatus(ctx, 2, mb.ID, MemberActive, mb.RowVersion); err == nil {
		t.Error("cross-tenant SetMemberStatus accepted")
	}
	su := &Subscription{EntityID: 1, MemberID: mb.ID, Year: "2026", Amount: 100}
	if err := m.CreateSubscription(ctx, su); err != nil {
		t.Fatalf("subscription: %v", err)
	}
	if subs, _ := m.SubscriptionsOf(ctx, 2, mb.ID); len(subs) != 0 {
		t.Fatalf("cross-tenant SubscriptionsOf=%d want empty", len(subs))
	}
	if _, err := m.SetSubscriptionStatus(ctx, 2, su.ID, SubValidated, su.RowVersion); err == nil {
		t.Error("cross-tenant SetSubscriptionStatus accepted")
	}
	if err := m.CreateSubscription(ctx, &Subscription{EntityID: 2, MemberID: mb.ID, Year: "2027", Amount: 100}); err == nil {
		t.Error("cross-tenant CreateSubscription accepted")
	}
	now := time.Now().UTC().Truncate(time.Second)
	d := &Donation{EntityID: 1, Ref: "X-D", DonorName: "G", Amount: 500,
		DonatedAt: now, Method: "transfer"}
	if err := m.CreateDonation(ctx, d); err != nil {
		t.Fatalf("donation: %v", err)
	}
	if _, err := m.SetDonationStatus(ctx, 2, d.ID, DonationPaid, d.RowVersion); err == nil {
		t.Error("cross-tenant SetDonationStatus accepted")
	}
}

func TestPGMemberFlow(t *testing.T) {
	ctx := context.Background()
	st := NewPGStore(pgtest.Pool(t))
	ty := &MemberType{EntityID: 1, Code: "PG", Label: "PG", AnnualFee: 100}
	if err := st.CreateType(ctx, ty); err != nil {
		t.Fatalf("type: %v", err)
	}
	mb := &Member{EntityID: 1, Ref: "PG-M", TypeID: ty.ID, FirstName: "A"}
	if err := st.CreateMember(ctx, mb); err != nil {
		t.Fatalf("member: %v", err)
	}
	if _, err := st.SetMemberStatus(ctx, 1, mb.ID, MemberActive, mb.RowVersion); err != nil {
		t.Fatalf("activate: %v", err)
	}
	d := &Donation{EntityID: 1, Ref: "PG-D", DonorName: "G", Amount: 500,
		DonatedAt: time.Now().UTC(), Method: "transfer"}
	if err := st.CreateDonation(ctx, d); err != nil {
		t.Fatalf("donation: %v", err)
	}
	if _, err := st.SetDonationStatus(ctx, 1, d.ID, DonationPaid, d.RowVersion); err != nil {
		t.Fatalf("pay: %v", err)
	}
}
