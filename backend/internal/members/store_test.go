package members

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
	upd, err := m.SetMemberStatus(ctx, mb.ID, MemberActive, mb.RowVersion)
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
	su2, err := m.SetSubscriptionStatus(ctx, su.ID, SubValidated, su.RowVersion)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if _, err := m.SetSubscriptionStatus(ctx, su.ID, SubPaid, su2.RowVersion); err != nil {
		t.Fatalf("pay: %v", err)
	}
	if _, err := m.SetMemberStatus(ctx, mb.ID, MemberResigned, upd.RowVersion); err != nil {
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
	upd, err := m.SetDonationStatus(ctx, d.ID, DonationPaid, d.RowVersion)
	if err != nil {
		t.Fatalf("pay: %v", err)
	}
	if _, err := m.SetDonationStatus(ctx, d.ID, DonationCanceled, upd.RowVersion); err == nil {
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
