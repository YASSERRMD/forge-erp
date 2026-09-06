package notify

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type failTwice struct{ calls int }

func (f *failTwice) Send(_ context.Context, _ OutboxItem) error {
	f.calls++
	if f.calls <= 2 {
		return errors.New("smtp down")
	}
	return nil
}

func TestDispatchRetry(t *testing.T) {
	now := time.Now().UTC()
	d := Dispatcher{Sender: &failTwice{}, Now: func() time.Time { return now }}
	item := &OutboxItem{Channel: "email", Recipient: "a@example.com"}
	if d.Dispatch(context.Background(), item) {
		t.Fatal("first attempt should fail")
	}
	if item.Attempts != 1 || item.SentAt != nil {
		t.Fatalf("after fail: %+v", item)
	}
	if d.Dispatch(context.Background(), item) {
		t.Fatal("second attempt should fail")
	}
	if !d.Dispatch(context.Background(), item) || item.SentAt == nil || item.Attempts != 2 {
		t.Fatalf("third attempt should send: %+v", item)
	}
}

func TestNextTryDeadLetter(t *testing.T) {
	now := time.Now().UTC()
	if _, ok := NextTry(now, MaxAttempts); ok {
		t.Fatal("should be dead-lettered at cap")
	}
	at, ok := NextTry(now, 0)
	if !ok || !at.After(now) {
		t.Fatal("first retry should schedule")
	}
}

func TestWebhookSignedDelivery(t *testing.T) {
	var gotSig string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-ForgeERP-Signature")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	wh := Webhook{Subject: "forgeerp.sales", URL: srv.URL, Secret: "s3cret", Enabled: true}
	code, err := Deliver(context.Background(), srv.Client(), wh, map[string]string{"event": "x"})
	if err != nil || code != http.StatusOK {
		t.Fatalf("deliver: %d %v", code, err)
	}
	if gotSig != Sign("s3cret", gotBody) {
		t.Fatal("signature mismatch")
	}
}

func TestRegistryDue(t *testing.T) {
	now := time.Now().UTC()
	r := NewRegistry()
	r.Register(&Job{Code: "a", Interval: time.Hour, NextRunAt: now.Add(-time.Minute), Enabled: true})
	r.Register(&Job{Code: "b", Interval: time.Hour, NextRunAt: now.Add(time.Hour), Enabled: true})
	due := r.Due(now)
	if len(due) != 1 || due[0] != "a" {
		t.Fatalf("due = %v", due)
	}
}
