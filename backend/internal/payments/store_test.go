package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"
)

func signPayload(secret, payload string, ts int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d.%s", ts, payload)
	return fmt.Sprintf("t=%d,v1=%s", ts, hex.EncodeToString(mac.Sum(nil)))
}

func TestStripeSignatureVectors(t *testing.T) {
	secret, payload := "whsec-test", `{"id":"evt_1"}`
	now := time.Unix(1786734000, 0)
	good := signPayload(secret, payload, now.Unix())
	if err := VerifyStripeSignature(secret, payload, good, 5*time.Minute, now); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	// Tampered payload.
	if err := VerifyStripeSignature(secret, `{"id":"evt_2"}`, good, 5*time.Minute, now); err == nil {
		t.Error("tampered payload accepted")
	}
	// Wrong secret.
	if err := VerifyStripeSignature("other", payload, good, 5*time.Minute, now); err == nil {
		t.Error("wrong secret accepted")
	}
	// Stale timestamp.
	old := signPayload(secret, payload, now.Add(-time.Hour).Unix())
	if err := VerifyStripeSignature(secret, payload, old, 5*time.Minute, now); err == nil {
		t.Error("stale webhook accepted")
	}
	// Malformed header.
	if err := VerifyStripeSignature(secret, payload, "garbage", 5*time.Minute, now); err == nil {
		t.Error("malformed header accepted")
	}
	// Registry always resolves manual; unknown rejected.
	reg := NewRegistry()
	if _, err := reg.Resolve(ProviderManual); err != nil {
		t.Fatalf("manual provider missing: %v", err)
	}
	if _, err := reg.Resolve("nope"); err == nil {
		t.Error("unknown provider resolved")
	}
}

func TestMemoryIdempotency(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	a := &PaymentAttempt{EntityID: 1, Ref: "ATT-1", OrgID: 7, Amount: 5000,
		Currency: "USD", Provider: ProviderStripe, WebhookKey: "evt_1"}
	if err := m.CreateAttempt(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	dup := &PaymentAttempt{EntityID: 1, Ref: "ATT-2", OrgID: 7, Amount: 5000,
		Currency: "USD", Provider: ProviderStripe, WebhookKey: "evt_1"}
	if err := m.CreateAttempt(ctx, dup); err == nil {
		t.Error("duplicate webhook key accepted")
	}
	got, ok := m.AttemptByWebhook(ctx, 1, "evt_1")
	if !ok || got.ID != a.ID {
		t.Fatalf("webhook lookup failed: %+v %v", got, ok)
	}
	upd, err := m.SetAttemptStatus(ctx, a.ID, AttemptSucceeded, a.RowVersion)
	if err != nil {
		t.Fatalf("succeed: %v", err)
	}
	if _, err := m.SetAttemptStatus(ctx, a.ID, AttemptFailed, upd.RowVersion); err == nil {
		t.Error("succeeded→failed accepted")
	}
	if _, err := m.SetAttemptStatus(ctx, a.ID, AttemptRefunded, upd.RowVersion); err != nil {
		t.Fatalf("refund: %v", err)
	}
}
