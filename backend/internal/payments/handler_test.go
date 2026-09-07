package payments

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func passthrough(_, _, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

func testRouter(secret string) http.Handler {
	r := chi.NewRouter()
	reg := NewRegistry(NewOnlineProvider(ProviderStripe), NewOnlineProvider(ProviderPayPal))
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Store: NewMemoryStore(), Providers: reg,
			WebhookSecret: secret, Tolerance: 5 * time.Minute}, passthrough)
	})
	return r
}

func TestIntentAndWebhookFlow(t *testing.T) {
	const secret = "whsec-test"
	h := testRouter(secret)

	// Manual intent settles immediately.
	raw, _ := json.Marshal(map[string]any{"org_id": 7, "amount": 2500, "currency": "USD", "provider": "manual"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/intents", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("manual intent: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Attempt PaymentAttempt `json:"attempt"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&created)
	if created.Attempt.Status != AttemptSucceeded {
		t.Fatalf("manual not settled: %+v", created.Attempt)
	}

	// Stripe intent stays pending with a client ref.
	raw, _ = json.Marshal(map[string]any{"org_id": 7, "amount": 2500, "currency": "USD", "provider": "stripe"})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/payments/intents", bytes.NewReader(raw))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("stripe intent: code=%d body=%s", rec.Code, rec.Body.String())
	}
	_ = json.NewDecoder(rec.Body).Decode(&created)
	if created.Attempt.Status != AttemptPending {
		t.Fatalf("stripe intent not pending: %+v", created.Attempt)
	}

	// Unknown provider → 422.
	raw, _ = json.Marshal(map[string]any{"org_id": 7, "amount": 1, "currency": "USD", "provider": "nope"})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/payments/intents", bytes.NewReader(raw))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown provider: code=%d want 422", rec.Code)
	}

	// Signed webhook succeeds the pending attempt.
	payload, _ := json.Marshal(map[string]any{"id": "evt_1", "attempt_id": created.Attempt.ID,
		"type": "payment_intent.succeeded"})
	now := time.Now().UTC()
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d.%s", now.Unix(), payload)
	sig := fmt.Sprintf("t=%d,v1=%s", now.Unix(), hex.EncodeToString(mac.Sum(nil)))
	post := func(body []byte, signature string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/payments/webhooks/stripe", bytes.NewReader(body))
		r.Header.Set("Stripe-Signature", signature)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, r)
		return rr
	}
	rec = post(payload, sig)
	if rec.Code != http.StatusOK {
		t.Fatalf("webhook: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var applied struct {
		Duplicate bool           `json:"duplicate"`
		Attempt   PaymentAttempt `json:"attempt"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&applied)
	if applied.Duplicate || applied.Attempt.Status != AttemptSucceeded {
		t.Fatalf("webhook not applied: %s", rec.Body.String())
	}
	// Redelivery → duplicate:true.
	rec = post(payload, sig)
	var redelivered struct {
		Duplicate bool `json:"duplicate"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&redelivered)
	if rec.Code != http.StatusOK || !redelivered.Duplicate {
		t.Fatalf("redelivery: code=%d body=%s", rec.Code, rec.Body.String())
	}
	// Bad signature → 401. Note: timestamp freshness is checked against the
	// real clock, so sign with the current time.
	rec = post(payload, "t=1,v1=deadbeef")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature: code=%d want 401", rec.Code)
	}
}

func TestWebhookDisabled(t *testing.T) {
	h := testRouter("")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/webhooks/stripe",
		bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled: code=%d want 503", rec.Code)
	}
}
