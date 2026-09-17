package mailing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/notify"
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

// fakeSender is a notify.Sender fake (notify used as a library only).
type fakeSender struct {
	mu   sync.Mutex
	fail map[string]bool
	sent []string
}

func (f *fakeSender) Send(_ context.Context, item notify.OutboxItem) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail[item.Recipient] {
		return errors.New("smtp: mailbox unavailable")
	}
	f.sent = append(f.sent, item.Recipient)
	return nil
}

func TestExpandAudience(t *testing.T) {
	members := []MemberMirror{
		{Email: "Ada@Example.COM ", TypeCode: "full"},
		{Email: "bob@example.com", TypeCode: "trial"},
		{Email: "unsub@example.com", TypeCode: "full", Unsubscribed: true},
		{Email: "not-an-email", TypeCode: "full"},
	}
	orgs := []OrgMirror{
		{Email: "sales@acme.test", Customer: true},
		{Email: "vendor@acme.test", Customer: false},
		{Email: "ada@example.com", Customer: true}, // dup of member (case-insensitive)
	}
	supp := map[string]bool{"bob@example.com": true}
	got := ExpandAudience(Audience{MemberType: "full", CustomersOnly: true,
		ExtraEmails: []string{"extra@example.com", "bob@example.com"}},
		members, orgs, supp)
	want := []string{"ada@example.com", "extra@example.com", "sales@acme.test"}
	if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", want) {
		t.Fatalf("expanded=%v want %v", got, want)
	}
	if got := ExpandAudience(Audience{}, nil, nil, nil); len(got) != 0 {
		t.Fatalf("empty audience=%v want empty", got)
	}
}

func TestCampaignQueueSendUnsubMemory(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	sender := &fakeSender{fail: map[string]bool{"bad@example.com": true}}
	var n int64
	svc := &Service{Store: m, Sender: sender, Mint: func() string { n++; return fmt.Sprintf("tok-%d", n) }}
	c := &Campaign{EntityID: 1, Subject: "News", Body: "hi"}
	if err := m.CreateCampaign(ctx, nil, c); err != nil {
		t.Fatalf("campaign: %v", err)
	}
	queued, err := svc.ExpandAndQueue(ctx, 1, c.ID, Audience{ExtraEmails: []string{"a@example.com", "bad@example.com"}},
		[]MemberMirror{{Email: "m@example.com", TypeCode: "x"}}, nil)
	if err != nil || queued != 3 {
		t.Fatalf("queued=%d err=%v want 3", queued, err)
	}
	// Re-queue is idempotent (duplicates skip, count 0 new).
	queued, err = svc.ExpandAndQueue(ctx, 1, c.ID, Audience{ExtraEmails: []string{"a@example.com"}}, nil, nil)
	if err != nil || queued != 0 {
		t.Fatalf("requeue=%d err=%v want 0", queued, err)
	}
	recs, _ := m.RecipientsOf(ctx, nil, 1, c.ID)
	var tok string
	for _, r := range recs {
		if r.Email == "m@example.com" {
			tok = r.Token
		}
		if r.Token == "" {
			t.Fatalf("recipient %s missing unsubscribe token", r.Email)
		}
	}
	if err := svc.Unsubscribe(ctx, tok); err != nil {
		t.Fatalf("unsub: %v", err)
	}
	// Suppressed address excluded from future expansions.
	queued, err = svc.ExpandAndQueue(ctx, 1, c.ID, Audience{ExtraEmails: []string{"m@example.com"}}, nil, nil)
	if err != nil || queued != 0 {
		t.Fatalf("suppressed requeue=%d err=%v want 0", queued, err)
	}
	sent, failed, err := svc.SendAll(ctx, 1, c.ID)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if sent != 1 || failed != 1 {
		t.Fatalf("sent=%d failed=%d want 1/1 (unsub skipped; bad mailbox fails)", sent, failed)
	}
	// Fresh campaign isolates statuses.
	c2 := &Campaign{EntityID: 1, Subject: "N2", Body: "b"}
	_ = m.CreateCampaign(ctx, nil, c2)
	if _, err := svc.ExpandAndQueue(ctx, 1, c2.ID, Audience{ExtraEmails: []string{"ok@example.com", "bad@example.com"}}, nil, nil); err != nil {
		t.Fatalf("queue2: %v", err)
	}
	sent, failed, err = svc.SendAll(ctx, 1, c2.ID)
	if err != nil || sent != 1 || failed != 1 {
		t.Fatalf("send2 sent=%d failed=%d err=%v want 1/1", sent, failed, err)
	}
	recs, _ = m.RecipientsOf(ctx, nil, 1, c2.ID)
	for _, r := range recs {
		if r.Email == "bad@example.com" && (r.Status != RecipientFailed || r.Error == "") {
			t.Fatalf("failed recipient=%+v want failed + error", r)
		}
	}
	// Cross-tenant isolation.
	if _, err := m.CampaignByID(ctx, nil, 2, c.ID); err == nil {
		t.Error("cross-tenant CampaignByID accepted")
	}
}

func TestMailingAPI(t *testing.T) {
	st := NewMemoryStore()
	svc := &Service{Store: st, Sender: &fakeSender{}}
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Store: st, Svc: svc}, passthrough)
	})
	post := func(path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	rec := post("/api/v1/mailing-campaigns", map[string]any{"subject": "Hi", "body": "hello"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("campaign: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var c Campaign
	_ = json.NewDecoder(rec.Body).Decode(&c)
	rec = post(fmt.Sprintf("/api/v1/mailing-campaigns/%d/queue", c.ID), map[string]any{
		"audience": map[string]any{"extra_emails": []string{"a@example.com"}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("queue: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = post(fmt.Sprintf("/api/v1/mailing-campaigns/%d/send", c.ID), map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("send: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var res map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&res)
	if res["sent"].(float64) != 1 {
		t.Fatalf("sent=%v want 1", res)
	}
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/mailing-campaigns/%d/recipients", c.ID), nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var list []Recipient
	_ = json.NewDecoder(rec.Body).Decode(&list)
	if len(list) != 1 || list[0].Status != RecipientSent {
		t.Fatalf("recipients=%+v", list)
	}
}

func TestPGMailingFlow(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	st := NewPGStore(pool)
	svc := &Service{Store: st, DB: pool, Sender: &fakeSender{}, Mint: func() string { return "tok-pg-1" }}
	c := &Campaign{EntityID: 1, Subject: "PG", Body: "b"}
	if err := st.CreateCampaign(ctx, pool, c); err != nil {
		t.Fatalf("campaign: %v", err)
	}
	queued, err := svc.ExpandAndQueue(ctx, 1, c.ID, Audience{ExtraEmails: []string{"pg@example.com"}}, nil, nil)
	if err != nil || queued != 1 {
		t.Fatalf("queued=%d err=%v", queued, err)
	}
	rec, err := st.RecipientByToken(ctx, pool, "tok-pg-1")
	if err != nil || rec.Email != "pg@example.com" {
		t.Fatalf("by token=%+v err=%v", rec, err)
	}
	sent, failed, err := svc.SendAll(ctx, 1, c.ID)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if sent != 1 || failed != 0 {
		t.Fatalf("sent=%d failed=%d want 1/0", sent, failed)
	}
}

func TestRecipientStatusIsEntityScoped(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	c := &Campaign{EntityID: 1, Subject: "Scoped", Body: "b"}
	if err := m.CreateCampaign(ctx, nil, c); err != nil {
		t.Fatal(err)
	}
	r := &Recipient{EntityID: 1, CampaignID: c.ID, Email: "s@example.com",
		Token: "tok-scope", Status: RecipientQueued}
	if err := m.AddRecipient(ctx, nil, r); err != nil {
		t.Fatal(err)
	}
	// Another entity cannot flip this entity's recipient.
	if err := m.SetRecipientStatus(ctx, nil, 2, r.ID, RecipientSent, ""); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("cross-entity status err=%v want ErrNotFound", err)
	}
	if err := m.SetRecipientStatus(ctx, nil, 1, r.ID, RecipientSent, ""); err != nil {
		t.Fatalf("own-entity status: %v", err)
	}
}
