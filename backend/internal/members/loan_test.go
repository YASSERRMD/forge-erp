package members

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/YASSERRMD/forge-erp/backend/internal/finance"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/workflow"
)

func TestBuildAmortisationFrench(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sched, err := BuildAmortisation(100000, 500, start, 12) // €1000 at 5% over 12 months
	if err != nil {
		t.Fatal(err)
	}
	if len(sched) != 12 {
		t.Fatalf("periods = %d", len(sched))
	}
	var princ, interest, paid int64
	for i, l := range sched {
		if l.Seq != i+1 {
			t.Fatalf("seq = %d want %d", l.Seq, i+1)
		}
		if l.Payment != l.Principal+l.Interest {
			t.Fatalf("line %d: payment %d != %d+%d", l.Seq, l.Payment, l.Principal, l.Interest)
		}
		if want := start.AddDate(0, i, 0); !l.DueDate.Equal(want) {
			t.Fatalf("line %d due %v want %v", l.Seq, l.DueDate, want)
		}
		princ += l.Principal
		interest += l.Interest
		paid += l.Payment
	}
	if princ != 100000 {
		t.Fatalf("principal legs sum to %d", princ)
	}
	if paid != princ+interest {
		t.Fatalf("paid %d != principal+interest %d", paid, princ+interest)
	}
	if sched[11].Remaining != 0 {
		t.Fatalf("last remaining = %d", sched[11].Remaining)
	}
	// French constant annuity: all but the absorbing last line are equal.
	for i := 0; i+2 < len(sched); i++ {
		if sched[i].Payment != sched[i+1].Payment {
			t.Fatalf("annuity varies: line %d = %d, line %d = %d",
				sched[i].Seq, sched[i].Payment, sched[i+1].Seq, sched[i+1].Payment)
		}
	}
	// Total-interest sanity for €1000 @ 5% / 12m (≈ €27.30).
	t.Logf("annuity=%d total_interest=%d", sched[0].Payment, interest)
	if interest <= 0 || interest > 5000 {
		t.Fatalf("total interest = %d", interest)
	}
	if sched[0].Payment != 8561 {
		t.Fatalf("annuity = %d want 8561", sched[0].Payment)
	}
	if interest != 2730 {
		t.Fatalf("total interest = %d want 2730", interest)
	}
}

func TestBuildAmortisationEdgeCases(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Zero rate degenerates to an even split with remainder last.
	zero, err := BuildAmortisation(10000, 0, start, 3)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, l := range zero {
		if l.Interest != 0 {
			t.Fatalf("zero-rate interest = %d", l.Interest)
		}
		total += l.Payment
	}
	if total != 10000 || zero[2].Remaining != 0 {
		t.Fatalf("zero-rate schedule=%+v", zero)
	}
	if zero[0].Payment != 3334 || zero[2].Payment != 3332 {
		t.Fatalf("zero-rate split=%d,%d,%d", zero[0].Payment, zero[1].Payment, zero[2].Payment)
	}
	// Single period: principal + one month of interest.
	one, err := BuildAmortisation(5000, 1000, start, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || one[0].Principal != 5000 || one[0].Remaining != 0 {
		t.Fatalf("single-period=%+v", one)
	}
	if one[0].Interest != 42 || one[0].Payment != 5042 { // (5000*1000+60000)/120000 = 42
		t.Fatalf("single-period interest=%d payment=%d", one[0].Interest, one[0].Payment)
	}
	// Bad terms rejected.
	for _, tc := range []struct {
		p int64
		r int
		n int
	}{{0, 500, 12}, {-1, 500, 12}, {1000, -1, 12}, {1000, 500, 0}, {1000, 500, 361}} {
		if _, err := BuildAmortisation(tc.p, tc.r, start, tc.n); err == nil {
			t.Errorf("accepted %+v", tc)
		}
	}
	if _, err := BuildAmortisation(1000, 500, time.Time{}, 12); err == nil {
		t.Error("accepted zero start")
	}
}

// fakeLedger validates entries like finance does and records them.
type fakeLedger struct {
	entries []*finance.Entry
	fail    error
}

func (f *fakeLedger) PostEntry(_ context.Context, _ platform.DBTX, e *finance.Entry) error {
	if f.fail != nil {
		return f.fail
	}
	if err := e.Validate(); err != nil {
		return err
	}
	e.ID = int64(len(f.entries) + 1)
	f.entries = append(f.entries, e)
	return nil
}

func TestMemberLoanFlowMemory(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	led := &fakeLedger{}
	l := &MemberLoan{EntityID: 1, Label: "Van", Principal: 120000, RateBps: 600,
		Start: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), Periods: 12}
	if err := m.CreateMemberLoan(ctx, nil, l); err != nil {
		t.Fatalf("create: %v", err)
	}
	sched, err := m.MemberLoanSchedule(ctx, nil, 1, l.ID)
	if err != nil || len(sched) != 12 {
		t.Fatalf("schedule=%d err=%v", len(sched), err)
	}
	if _, err := m.MemberLoanSchedule(ctx, nil, 2, l.ID); err == nil {
		t.Error("cross-tenant schedule accepted")
	}
	// Disbursement posts DR receivable / CR bank.
	disb, err := PostLoanDisbursement(ctx, nil, led, 1, 9, *l, 411, 512, time.Now().UTC(), nil)
	if err != nil {
		t.Fatalf("disburse post: %v", err)
	}
	if disb.Lines[0].Debit != 120000 || disb.Lines[1].Credit != 120000 {
		t.Fatalf("disbursement legs=%+v", disb.Lines)
	}
	if _, err := m.SetMemberLoanStatus(ctx, nil, 1, l.ID, MemberLoanDisbursed, l.RowVersion); err != nil {
		t.Fatalf("disburse status: %v", err)
	}
	// Repay line 1: DR bank / CR receivable + CR interest.
	rep, err := PostLoanRepayment(ctx, nil, led, 1, 9, *l, sched[0], 512, 411, 763, time.Now().UTC(), nil)
	if err != nil {
		t.Fatalf("repay post: %v", err)
	}
	if rep.Lines[0].Debit != sched[0].Payment || rep.Lines[1].Credit != sched[0].Principal {
		t.Fatalf("repayment legs=%+v", rep.Lines)
	}
	if _, err := m.MarkLoanLinePaid(ctx, nil, 1, l.ID, 1, time.Now().UTC()); err != nil {
		t.Fatalf("mark paid: %v", err)
	}
	if _, err := m.MarkLoanLinePaid(ctx, nil, 1, l.ID, 1, time.Now().UTC()); !errors.Is(err, platform.ErrConflict) {
		t.Fatalf("double pay: %v", err)
	}
	if _, err := PostLoanRepayment(ctx, nil, led, 1, 9, *l,
		MemberLoanLine{Paid: true}, 512, 411, 763, time.Now().UTC(), nil); !errors.Is(err, platform.ErrConflict) {
		t.Fatalf("paid-line repost not a conflict: %v", err)
	}
	// Illegal loan transition rejected.
	if _, err := m.SetMemberLoanStatus(ctx, nil, 1, l.ID, MemberLoanDraft, l.RowVersion+1); err == nil {
		t.Error("backward loan transition accepted")
	}
}

func TestPGMemberLoanFlow(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	st := NewPGStore(pool)
	l := &MemberLoan{EntityID: 1, Label: "PG-Loan", Principal: 60000, RateBps: 300,
		Start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Periods: 6}
	if err := st.CreateMemberLoan(ctx, pool, l); err != nil {
		t.Fatalf("create: %v", err)
	}
	sched, err := st.MemberLoanSchedule(ctx, pool, 1, l.ID)
	if err != nil || len(sched) != 6 {
		t.Fatalf("schedule=%d err=%v", len(sched), err)
	}
	var princ int64
	for _, li := range sched {
		princ += li.Principal
	}
	if princ != 60000 || sched[5].Remaining != 0 {
		t.Fatalf("persisted schedule princ=%d last=%+v", princ, sched[5])
	}
	if _, err := st.MarkLoanLinePaid(ctx, pool, 1, l.ID, 1, time.Now().UTC()); err != nil {
		t.Fatalf("mark paid: %v", err)
	}
	if _, err := st.MarkLoanLinePaid(ctx, pool, 1, l.ID, 1, time.Now().UTC()); !errors.Is(err, platform.ErrConflict) {
		t.Fatalf("double pay PG: %v", err)
	}
	if _, err := st.SetMemberLoanStatus(ctx, pool, 1, l.ID, MemberLoanDisbursed, l.RowVersion); err != nil {
		t.Fatalf("disburse: %v", err)
	}
}

func TestDonationReceiptViaRegistry(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	now := time.Now().UTC().Truncate(time.Second)
	d := &Donation{EntityID: 1, Ref: "DON-R", DonorName: "Ada L.",
		Amount: 25000, DonatedAt: now, Method: "transfer"}
	if err := m.CreateDonation(ctx, nil, d); err != nil {
		t.Fatal(err)
	}
	// Promised donations have no receipt.
	if _, _, err := RenderDonationReceipt(ctx, *d, "en"); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("promised receipt: %v", err)
	}
	upd, err := m.SetDonationStatus(ctx, nil, 1, d.ID, DonationPaid, d.RowVersion)
	if err != nil {
		t.Fatal(err)
	}
	reader, ctype, err := RenderDonationReceipt(ctx, upd, "en")
	if err != nil {
		t.Fatal(err)
	}
	if ctype != "application/pdf" {
		t.Fatalf("content type = %q", ctype)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(reader); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("%PDF")) {
		t.Fatal("receipt is not a PDF")
	}
	// Deterministic: identical input renders identical bytes.
	r2, _, err := RenderDonationReceipt(ctx, upd, "en")
	if err != nil {
		t.Fatal(err)
	}
	var buf2 bytes.Buffer
	_, _ = buf2.ReadFrom(r2)
	if !bytes.Equal(buf.Bytes(), buf2.Bytes()) {
		t.Fatal("receipt not deterministic")
	}
}

func TestSubscriptionWorkflowFiring(t *testing.T) {
	st := NewMemoryStore()
	bus := platform.NewMemoryBus()
	var got []platform.Event
	bus.Subscribe("forgeerp.members.subscription.validated.v1", func(_ context.Context, e platform.Event) {
		got = append(got, e)
	})
	bus.Subscribe("forgeerp.members.subscription.paid.v1", func(_ context.Context, e platform.Event) {
		got = append(got, e)
	})
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Store: st, Bus: bus}, passthrough)
	})
	post := func(path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	ctx := context.Background()
	ty := &MemberType{EntityID: 1, Code: "WF", Label: "WF", AnnualFee: 100}
	if err := st.CreateType(ctx, nil, ty); err != nil {
		t.Fatal(err)
	}
	mb := &Member{EntityID: 1, Ref: "WF-1", TypeID: ty.ID, FirstName: "A"}
	if err := st.CreateMember(ctx, nil, mb); err != nil {
		t.Fatal(err)
	}
	su := &Subscription{EntityID: 1, MemberID: mb.ID, Year: "2026", Amount: 100}
	if err := st.CreateSubscription(ctx, nil, su); err != nil {
		t.Fatal(err)
	}
	// draft → validated fires the validated hook with merged payload.
	subs, _ := st.SubscriptionsOf(ctx, nil, 1, mb.ID)
	if len(subs) != 1 {
		t.Fatalf("subs=%d", len(subs))
	}
	rec := post("/api/v1/subscriptions/"+itoa(subs[0].ID)+"/status",
		map[string]any{"status": 1, "row_version": subs[0].RowVersion})
	if rec.Code != http.StatusOK {
		t.Fatalf("validate: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(got) != 1 || got[0].Subject != "forgeerp.members.subscription.validated.v1" {
		t.Fatalf("events=%+v", got)
	}
	// validated → paid fires paid hook with the field-set-merged payload.
	var cur Subscription
	_ = json.NewDecoder(rec.Body).Decode(&cur)
	rec = post("/api/v1/subscriptions/"+itoa(subs[0].ID)+"/status",
		map[string]any{"status": 2, "row_version": cur.RowVersion})
	if rec.Code != http.StatusOK {
		t.Fatalf("pay: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(got) != 2 || got[1].Subject != "forgeerp.members.subscription.paid.v1" {
		t.Fatalf("events=%+v", got)
	}
	if got[1].Payload["settled"] != true {
		t.Fatalf("paid payload=%v (field-set missing)", got[1].Payload)
	}
	_ = rec
}

func TestSubscriptionWorkflowNoMatch(t *testing.T) {
	st := NewMemoryStore()
	bus := platform.NewMemoryBus()
	fired := 0
	bus.Subscribe("forgeerp.members.subscription.validated.v1", func(context.Context, platform.Event) { fired++ })
	empty, err := workflow.New()
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Store: st, Bus: bus, Workflow: empty}, passthrough)
	})
	ctx := context.Background()
	ty := &MemberType{EntityID: 1, Code: "NM", Label: "NM", AnnualFee: 100}
	_ = st.CreateType(ctx, nil, ty)
	mb := &Member{EntityID: 1, Ref: "NM-1", TypeID: ty.ID, FirstName: "A"}
	_ = st.CreateMember(ctx, nil, mb)
	su := &Subscription{EntityID: 1, MemberID: mb.ID, Year: "2026", Amount: 100}
	_ = st.CreateSubscription(ctx, nil, su)
	subs, _ := st.SubscriptionsOf(ctx, nil, 1, mb.ID)
	raw, _ := json.Marshal(map[string]any{"status": 1, "row_version": subs[0].RowVersion})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/subscriptions/"+itoa(subs[0].ID)+"/status", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("validate: code=%d", rec.Code)
	}
	if fired != 0 {
		t.Fatal("empty engine fired a hook")
	}
}

func TestMemberLoanAPI(t *testing.T) {
	st := NewMemoryStore()
	led := &fakeLedger{}
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Store: st, Bus: platform.NewMemoryBus(), Ledger: led}, passthrough)
	})
	post := func(path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	rec := post("/api/v1/member-loans", map[string]any{
		"label": "API-Loan", "principal": 12000, "rate_bps": 600,
		"start": time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "periods": 4})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create loan: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Loan     MemberLoan       `json:"loan"`
		Schedule []MemberLoanLine `json:"schedule"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&created)
	if len(created.Schedule) != 4 {
		t.Fatalf("schedule=%d", len(created.Schedule))
	}
	id := itoa(created.Loan.ID)
	if got := get("/api/v1/member-loans/" + id + "/schedule"); got.Code != http.StatusOK {
		t.Fatalf("schedule get: code=%d", got.Code)
	}
	// Disburse without ledger configured → 501.
	r2 := chi.NewRouter()
	r2.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Store: st}, passthrough)
	})
	raw, _ := json.Marshal(map[string]any{"journal_id": 1, "receivable_account": 411,
		"bank_account": 512, "row_version": created.Loan.RowVersion})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/member-loans/"+id+"/disburse", bytes.NewReader(raw))
	noled := httptest.NewRecorder()
	r2.ServeHTTP(noled, req)
	if noled.Code != http.StatusNotImplemented {
		t.Fatalf("no-ledger disburse: code=%d", noled.Code)
	}
	rec = post("/api/v1/member-loans/"+id+"/disburse", map[string]any{
		"journal_id": 1, "receivable_account": 411, "bank_account": 512,
		"row_version": created.Loan.RowVersion})
	if rec.Code != http.StatusCreated {
		t.Fatalf("disburse: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(led.entries) != 1 {
		t.Fatalf("ledger entries=%d", len(led.entries))
	}
	// Repay installment 1, then double-pay → 409.
	rec = post("/api/v1/member-loans/"+id+"/repay", map[string]any{
		"seq": 1, "journal_id": 1, "bank_account": 512,
		"receivable_account": 411, "interest_account": 763})
	if rec.Code != http.StatusCreated {
		t.Fatalf("repay: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = post("/api/v1/member-loans/"+id+"/repay", map[string]any{
		"seq": 1, "journal_id": 1, "bank_account": 512,
		"receivable_account": 411, "interest_account": 763})
	if rec.Code != http.StatusConflict {
		t.Fatalf("double repay: code=%d want 409 body=%s", rec.Code, rec.Body.String())
	}
	// Repay the rest → loan moves to repaid.
	for _, seq := range []int{2, 3, 4} {
		rec = post("/api/v1/member-loans/"+id+"/repay", map[string]any{
			"seq": seq, "journal_id": 1, "bank_account": 512,
			"receivable_account": 411, "interest_account": 763})
		if rec.Code != http.StatusCreated {
			t.Fatalf("repay %d: code=%d body=%s", seq, rec.Code, rec.Body.String())
		}
	}
	var done struct {
		Loan MemberLoan `json:"loan"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&done)
	if done.Loan.Status != MemberLoanRepaid {
		t.Fatalf("final status=%d want repaid", done.Loan.Status)
	}
	// Donation receipt endpoint: promised → 422, paid → 200 PDF.
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	d := &Donation{EntityID: 1, Ref: "API-DON", DonorName: "Grace",
		Amount: 5000, DonatedAt: now, Method: "cash"}
	if err := st.CreateDonation(ctx, nil, d); err != nil {
		t.Fatal(err)
	}
	if got := get("/api/v1/donations/" + itoa(d.ID) + "/receipt"); got.Code != http.StatusUnprocessableEntity {
		t.Fatalf("promised receipt: code=%d want 422", got.Code)
	}
	if _, err := st.SetDonationStatus(ctx, nil, 1, d.ID, DonationPaid, d.RowVersion); err != nil {
		t.Fatal(err)
	}
	got := get("/api/v1/donations/" + itoa(d.ID) + "/receipt")
	if got.Code != http.StatusOK || got.Header().Get("Content-Type") != "application/pdf" {
		t.Fatalf("receipt: code=%d ctype=%s", got.Code, got.Header().Get("Content-Type"))
	}
	if !bytes.HasPrefix(got.Body.Bytes(), []byte("%PDF")) {
		t.Fatal("receipt body is not a PDF")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
