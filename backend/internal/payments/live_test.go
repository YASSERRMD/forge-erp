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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// --- httptest doubles -------------------------------------------------

// stripeDouble serves payment-intent creation and refunds with canned ids.
func stripeDouble(t *testing.T, wantSecret string, seenAuth *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+wantSecret {
			http.Error(w, `{"error":{"message":"bad auth"}}`, http.StatusUnauthorized)
			return
		}
		if seenAuth != nil {
			*seenAuth = r.Header.Get("Authorization")
		}
		switch r.URL.Path {
		case "/v1/payment_intents":
			_ = r.ParseForm()
			if r.Form.Get("amount") == "" || r.Form.Get("currency") == "" {
				http.Error(w, `{"error":{"message":"missing params"}}`, http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "pi_test123", "client_secret": "pi_test123_secret_abc", "status": "requires_payment_method",
			})
		case "/v1/refunds":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "re_test1", "status": "succeeded"})
		default:
			http.Error(w, `{"error":{"message":"not found"}}`, http.StatusNotFound)
		}
	}))
}

type paypalDouble struct {
	server     *httptest.Server
	tokenCalls *int64
	verifyMode string // "SUCCESS" | "FAILURE"
	orderSeq   *int64
}

// newPayPalDouble serves token, orders, capture, refund and remote webhook
// verification. It asserts Basic auth on the token call. Order ids are
// unique per creation call (ORDER-1, ORDER-2, ...) so repeated intents never
// collide; ORDER-404 is explicitly unknown (404) for negative-path tests.
func newPayPalDouble(t *testing.T, clientID, secret string) *paypalDouble {
	t.Helper()
	var tokenCalls, orderSeq int64
	d := &paypalDouble{tokenCalls: &tokenCalls, verifyMode: "SUCCESS", orderSeq: &orderSeq}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != clientID || p != secret {
			http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
			return
		}
		atomic.AddInt64(d.tokenCalls, 1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok-test", "token_type": "Bearer", "expires_in": 3600,
		})
	})
	mux.HandleFunc("/v2/checkout/orders", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-test" {
			http.Error(w, `{"message":"bad token"}`, http.StatusUnauthorized)
			return
		}
		n := atomic.AddInt64(d.orderSeq, 1)
		id := fmt.Sprintf("ORDER-%d", n)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": id,
			"links": []map[string]string{
				{"href": "https://example.test/approve/" + id, "rel": "approve"},
			},
		})
	})
	orderDoc := func(id, capID string) map[string]any {
		return map[string]any{
			"id": id,
			"purchase_units": []map[string]any{{
				"amount":   map[string]string{"currency_code": "USD", "value": "25.00"},
				"payments": map[string]any{"captures": []map[string]string{{"id": capID}}},
			}},
		}
	}
	mux.HandleFunc("/v2/checkout/orders/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/v2/checkout/orders/")
		if strings.HasSuffix(rest, "/capture") && r.Method == http.MethodPost {
			_ = json.NewEncoder(w).Encode(orderDoc(strings.TrimSuffix(rest, "/capture"), "CAP-9"))
			return
		}
		if r.Method == http.MethodGet && !strings.Contains(rest, "/") {
			if rest == "ORDER-404" {
				http.Error(w, `{"message":"no such order"}`, http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(orderDoc(rest, "CAP-1"))
			return
		}
		http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
	})
	mux.HandleFunc("/v2/payments/captures/CAP-1/refund", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "REF-1", "status": "COMPLETED"})
	})
	mux.HandleFunc("/v1/notifications/verify-webhook-signature", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"verification_status": d.verifyMode})
	})
	d.server = httptest.NewServer(mux)
	return d
}

// --- Stripe live client -----------------------------------------------

func TestStripeMintFallback(t *testing.T) {
	c := NewStripeClient(StripeConfig{})
	if c.Live() {
		t.Fatal("empty secret must be mint-only")
	}
	in, err := c.CreateIntent(t.Context(), 2500, "USD")
	if err != nil {
		t.Fatalf("mint intent: %v", err)
	}
	if !strings.HasPrefix(in.Ref, "stripe-pi-") || in.ClientSecret != "" {
		t.Fatalf("bad mint intent: %+v", in)
	}
	ref, err := c.Refund(t.Context(), in.Ref, 2500)
	if err != nil || !strings.HasPrefix(ref, "stripe-re-") {
		t.Fatalf("mint refund: %q %v", ref, err)
	}
	if _, err := c.CreateIntent(t.Context(), 0, "USD"); err == nil {
		t.Error("zero amount accepted")
	}
}

func TestStripeLiveIntentAndRefund(t *testing.T) {
	var auth string
	srv := stripeDouble(t, "sk_test_123", &auth)
	defer srv.Close()
	c := NewStripeClient(StripeConfig{SecretKey: "sk_test_123", BaseURL: srv.URL})
	if !c.Live() {
		t.Fatal("configured secret must be live")
	}
	in, err := c.CreateIntent(t.Context(), 2500, "usd")
	if err != nil {
		t.Fatalf("live intent: %v", err)
	}
	if in.Ref != "pi_test123" || in.ClientSecret != "pi_test123_secret_abc" {
		t.Fatalf("bad live intent: %+v", in)
	}
	if auth != "Bearer sk_test_123" {
		t.Fatalf("auth header not forwarded: %q", auth)
	}
	ref, err := c.Refund(t.Context(), "pi_test123", 2500)
	if err != nil || ref != "re_test1" {
		t.Fatalf("live refund: %q %v", ref, err)
	}
}

func TestStripeLiveErrorRedacted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"card declined"}}`, http.StatusPaymentRequired)
	}))
	defer srv.Close()
	c := NewStripeClient(StripeConfig{SecretKey: "sk_live_xxx", BaseURL: srv.URL})
	_, err := c.CreateIntent(t.Context(), 100, "USD")
	if err == nil {
		t.Fatal("expected provider error")
	}
	if !strings.Contains(err.Error(), "402") || strings.Contains(err.Error(), "sk_live_xxx") {
		t.Fatalf("error must carry status, never the secret: %v", err)
	}
}

// --- PayPal live client ------------------------------------------------

func TestPayPalMintFallback(t *testing.T) {
	c := NewPayPalClient(PayPalConfig{})
	if c.Live() {
		t.Fatal("empty creds must be mint-only")
	}
	in, err := c.CreateIntent(t.Context(), 2500, "USD")
	if err != nil {
		t.Fatalf("mint intent: %v", err)
	}
	if !strings.HasPrefix(in.Ref, "paypal-pi-") || in.ApprovalURL != "" {
		t.Fatalf("bad mint intent: %+v", in)
	}
}

func TestPayPalLiveOrderCaptureRefund(t *testing.T) {
	d := newPayPalDouble(t, "pp-id", "pp-secret")
	defer d.server.Close()
	c := NewPayPalClient(PayPalConfig{ClientID: "pp-id", ClientSecret: "pp-secret", BaseURL: d.server.URL})
	in, err := c.CreateIntent(t.Context(), 2500, "USD")
	if err != nil {
		t.Fatalf("order create: %v", err)
	}
	if in.Ref != "ORDER-1" || in.ApprovalURL != "https://example.test/approve/ORDER-1" {
		t.Fatalf("bad order intent: %+v", in)
	}
	capID, err := c.Capture(t.Context(), "ORDER-1")
	if err != nil || capID != "CAP-9" {
		t.Fatalf("capture: %q %v", capID, err)
	}
	ref, err := c.Refund(t.Context(), "ORDER-1", 2500)
	if err != nil || ref != "REF-1" {
		t.Fatalf("refund: %q %v", ref, err)
	}
	if got := atomic.LoadInt64(d.tokenCalls); got != 1 {
		t.Fatalf("token calls=%d want 1 (cached)", got)
	}
}

func TestPayPalRefundWithoutCapture(t *testing.T) {
	d := newPayPalDouble(t, "pp-id", "pp-secret")
	defer d.server.Close()
	c := NewPayPalClient(PayPalConfig{ClientID: "pp-id", ClientSecret: "pp-secret", BaseURL: d.server.URL})
	// ORDER-1 carries a capture: partial refund succeeds through the chain.
	if ref, err := c.Refund(t.Context(), "ORDER-1", 100); err != nil || ref != "REF-1" {
		t.Fatalf("chained refund: %q %v", ref, err)
	}
	// Unknown order (double answers 404) → error, never a minted ref.
	if ref, err := c.Refund(t.Context(), "ORDER-404", 100); err == nil {
		t.Fatalf("uncapturable order refunded: %q", ref)
	}
	if _, err := c.Capture(t.Context(), ""); err == nil {
		t.Error("empty order capture accepted")
	}
	if _, err := c.Refund(t.Context(), "", 100); err == nil {
		t.Error("empty order refund accepted")
	}
}

func TestPayPalAmountVectors(t *testing.T) {
	for _, tc := range []struct {
		cur  string
		min  int64
		want string
	}{
		{"USD", 2500, "25.00"},
		{"USD", 5, "0.05"},
		{"EUR", 100, "1.00"},
		{"JPY", 500, "500"},
		{"KRW", 1000, "1000"},
	} {
		got, err := paypalAmount(tc.cur, tc.min)
		if err != nil || got != tc.want {
			t.Errorf("paypalAmount(%s,%d)=%q,%v want %q", tc.cur, tc.min, got, err, tc.want)
		}
	}
	if _, err := paypalAmount("USD", 0); err == nil {
		t.Error("zero amount accepted")
	}
	if _, err := paypalAmount("", 100); err == nil {
		t.Error("empty currency accepted")
	}
}

func TestPayPalSignatureVectors(t *testing.T) {
	secret := "whsec-pp"
	now := time.Unix(1786734000, 0).UTC()
	hdr := PayPalWebhookHeaders{
		TransmissionID:   "tx-1",
		TransmissionTime: now.Format(time.RFC3339),
	}
	payload := `{"id":"evt_pp_1"}`
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%s.%s.%s", hdr.TransmissionID, hdr.TransmissionTime, payload)
	hdr.Signature = hex.EncodeToString(mac.Sum(nil))
	if err := VerifyPayPalSignature(secret, hdr, payload, 5*time.Minute, now); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	// Uppercase hex must also verify (constant-time compare on normalized form).
	upper := hdr
	upper.Signature = strings.ToUpper(hdr.Signature)
	if err := VerifyPayPalSignature(secret, upper, payload, 5*time.Minute, now); err != nil {
		t.Fatalf("uppercase signature rejected: %v", err)
	}
	// Tampered payload.
	if err := VerifyPayPalSignature(secret, hdr, `{"id":"evt_pp_2"}`, 5*time.Minute, now); err == nil {
		t.Error("tampered payload accepted")
	}
	// Wrong secret.
	if err := VerifyPayPalSignature("other", hdr, payload, 5*time.Minute, now); err == nil {
		t.Error("wrong secret accepted")
	}
	// Stale timestamp.
	stale := hdr
	stale.TransmissionTime = now.Add(-time.Hour).Format(time.RFC3339)
	if err := VerifyPayPalSignature(secret, stale, payload, 5*time.Minute, now); err == nil {
		t.Error("stale webhook accepted")
	}
	// Missing headers.
	if err := VerifyPayPalSignature(secret, PayPalWebhookHeaders{}, payload, 5*time.Minute, now); err == nil {
		t.Error("missing headers accepted")
	}
	// Unconfigured secret.
	if err := VerifyPayPalSignature("", hdr, payload, 5*time.Minute, now); err == nil {
		t.Error("empty secret accepted")
	}
	// Bad timestamp format.
	bad := hdr
	bad.TransmissionTime = "not-a-time"
	if err := VerifyPayPalSignature(secret, bad, payload, 5*time.Minute, now); err == nil {
		t.Error("bad timestamp accepted")
	}
}

func TestPayPalRemoteVerify(t *testing.T) {
	d := newPayPalDouble(t, "pp-id", "pp-secret")
	defer d.server.Close()
	c := NewPayPalClient(PayPalConfig{ClientID: "pp-id", ClientSecret: "pp-secret",
		WebhookID: "WH-1", BaseURL: d.server.URL})
	hdr := PayPalWebhookHeaders{TransmissionID: "tx-1", TransmissionTime: "2026-01-01T00:00:00Z", Signature: "sig"}
	if !c.RemoteVerify() {
		t.Fatal("expected remote verify configured")
	}
	if err := c.VerifyWebhookRemote(t.Context(), hdr, []byte(`{"id":"evt_1"}`)); err != nil {
		t.Fatalf("remote verify: %v", err)
	}
	d.verifyMode = "FAILURE"
	if err := c.VerifyWebhookRemote(t.Context(), hdr, []byte(`{"id":"evt_1"}`)); err == nil {
		t.Error("failed verification accepted")
	}
	plain := NewPayPalClient(PayPalConfig{})
	if plain.RemoteVerify() {
		t.Error("unconfigured client must not remote-verify")
	}
	if err := plain.VerifyWebhookRemote(t.Context(), hdr, []byte(`{}`)); err == nil {
		t.Error("unconfigured remote verify accepted")
	}
}

// --- handler surface: refund + paypal webhook ---------------------------

func liveRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, d, passthrough)
	})
	return r
}

func createAttemptViaAPI(t *testing.T, h http.Handler, provider string, amount int64) PaymentAttempt {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"org_id": 7, "amount": amount, "currency": "USD", "provider": provider})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/intents", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("intent %s: code=%d body=%s", provider, rec.Code, rec.Body.String())
	}
	var created struct {
		Attempt      PaymentAttempt `json:"attempt"`
		ClientRef    string         `json:"client_ref"`
		ClientSecret string         `json:"client_secret"`
		ApprovalURL  string         `json:"approval_url"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&created)
	return created.Attempt
}

func postRefund(t *testing.T, h http.Handler, id, rowVersion int64, amount *int64) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"row_version": rowVersion, "amount": amount})
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/v1/payments/attempts/%d/refund", id), bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRefundMintMode(t *testing.T) {
	reg := NewRegistry(NewOnlineProvider(ProviderStripe), NewOnlineProvider(ProviderPayPal))
	h := liveRouter(Deps{Store: NewMemoryStore(), Providers: reg,
		WebhookSecret: "whsec-test", PayPalWebhookSecret: "whsec-pp", Tolerance: 5 * time.Minute})

	// Manual intent settles immediately → refundable.
	a := createAttemptViaAPI(t, h, "manual", 2500)
	rec := postRefund(t, h, a.ID, a.RowVersion, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("refund: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Duplicate bool           `json:"duplicate"`
		Attempt   PaymentAttempt `json:"attempt"`
		Live      bool           `json:"live"`
		RefundRef string         `json:"refund_ref"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&out)
	if out.Duplicate || out.Live || out.Attempt.Status != AttemptRefunded || out.RefundRef == "" {
		t.Fatalf("bad mint refund: %s", rec.Body.String())
	}
	// Repeat → duplicate:true.
	rec = postRefund(t, h, out.Attempt.ID, out.Attempt.RowVersion, nil)
	var dup struct {
		Duplicate bool `json:"duplicate"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&dup)
	if rec.Code != http.StatusOK || !dup.Duplicate {
		t.Fatalf("repeat refund: code=%d body=%s", rec.Code, rec.Body.String())
	}
	// Pending attempt → 422.
	p := createAttemptViaAPI(t, h, "stripe", 100)
	if rec := postRefund(t, h, p.ID, p.RowVersion, nil); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("pending refund: code=%d want 422", rec.Code)
	}
	// Partial amount accepted (status still terminal Refunded by design).
	m := createAttemptViaAPI(t, h, "manual", 1000)
	half := int64(400)
	if rec := postRefund(t, h, m.ID, m.RowVersion, &half); rec.Code != http.StatusOK {
		t.Fatalf("partial refund: code=%d body=%s", rec.Code, rec.Body.String())
	}
	// Over-amount → 422.
	m2 := createAttemptViaAPI(t, h, "manual", 1000)
	over := int64(1001)
	if rec := postRefund(t, h, m2.ID, m2.RowVersion, &over); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("over refund: code=%d want 422", rec.Code)
	}
	// Unknown attempt → 404; bad id → 400.
	if rec := postRefund(t, h, 9999, 1, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown refund: code=%d want 404", rec.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/attempts/abc/refund",
		bytes.NewReader([]byte(`{"row_version":1}`)))
	bare := httptest.NewRecorder()
	h.ServeHTTP(bare, req)
	if bare.Code != http.StatusBadRequest {
		t.Fatalf("bad id: code=%d want 400", bare.Code)
	}
}

func TestRefundLiveStripe(t *testing.T) {
	srv := stripeDouble(t, "sk_test_123", nil)
	defer srv.Close()
	reg := NewRegistry(NewOnlineProvider(ProviderStripe))
	h := liveRouter(Deps{Store: NewMemoryStore(), Providers: reg,
		Stripe: NewStripeClient(StripeConfig{SecretKey: "sk_test_123", BaseURL: srv.URL}),
		WebhookSecret: "whsec-test", Tolerance: 5 * time.Minute})

	// Live intent carries the provider ref + client secret, keyed for webhooks.
	raw, _ := json.Marshal(map[string]any{"org_id": 7, "amount": 2500, "currency": "USD", "provider": "stripe"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/intents", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("live intent: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Attempt      PaymentAttempt `json:"attempt"`
		ClientSecret string         `json:"client_secret"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&created)
	if created.Attempt.Ref != "pi_test123" || created.ClientSecret == "" {
		t.Fatalf("bad live intent response: %s", rec.Body.String())
	}
	if created.Attempt.WebhookKey != "pi_test123" {
		t.Fatalf("live ref must be webhook-keyed: %+v", created.Attempt)
	}
	// Webhook-settle then live-refund.
	payload, _ := json.Marshal(map[string]any{"id": "evt_live", "attempt_id": created.Attempt.ID,
		"type": "payment_intent.succeeded"})
	now := time.Now().UTC()
	mac := hmac.New(sha256.New, []byte("whsec-test"))
	fmt.Fprintf(mac, "%d.%s", now.Unix(), payload)
	sig := fmt.Sprintf("t=%d,v1=%s", now.Unix(), hex.EncodeToString(mac.Sum(nil)))
	wreq := httptest.NewRequest(http.MethodPost, "/api/v1/payments/webhooks/stripe", bytes.NewReader(payload))
	wreq.Header.Set("Stripe-Signature", sig)
	wreq = wreq.WithContext(platform.ContextWithEntity(wreq.Context(), 1))
	wrec := httptest.NewRecorder()
	h.ServeHTTP(wrec, wreq)
	if wrec.Code != http.StatusOK {
		t.Fatalf("settle: code=%d body=%s", wrec.Code, wrec.Body.String())
	}
	var settled struct {
		Attempt PaymentAttempt `json:"attempt"`
	}
	_ = json.NewDecoder(wrec.Body).Decode(&settled)
	rec = postRefund(t, h, settled.Attempt.ID, settled.Attempt.RowVersion, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("live refund: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Live      bool   `json:"live"`
		RefundRef string `json:"refund_ref"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&out)
	if !out.Live || out.RefundRef != "re_test1" {
		t.Fatalf("bad live refund: %s", rec.Body.String())
	}
}

// signPayPal builds a test-double signature header set for payload.
func signPayPal(secret, payload string, when time.Time) PayPalWebhookHeaders {
	hdr := PayPalWebhookHeaders{TransmissionID: "tx-test", TransmissionTime: when.UTC().Format(time.RFC3339)}
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%s.%s.%s", hdr.TransmissionID, hdr.TransmissionTime, payload)
	hdr.Signature = hex.EncodeToString(mac.Sum(nil))
	return hdr
}

func postPayPalWebhook(t *testing.T, h http.Handler, body []byte, hdr PayPalWebhookHeaders, withEntity bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/webhooks/paypal", bytes.NewReader(body))
	req.Header.Set("PAYPAL-TRANSMISSION-ID", hdr.TransmissionID)
	req.Header.Set("PAYPAL-TRANSMISSION-TIME", hdr.TransmissionTime)
	req.Header.Set("PAYPAL-TRANSMISSION-SIG", hdr.Signature)
	if withEntity {
		req = req.WithContext(platform.ContextWithEntity(req.Context(), 1))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPayPalWebhookFlow(t *testing.T) {
	const secret = "whsec-pp"
	reg := NewRegistry(NewOnlineProvider(ProviderStripe), NewOnlineProvider(ProviderPayPal))
	h := liveRouter(Deps{Store: NewMemoryStore(), Providers: reg,
		WebhookSecret: "whsec-test", PayPalWebhookSecret: secret, Tolerance: 5 * time.Minute})

	a := createAttemptViaAPI(t, h, "paypal", 2500)
	payload, _ := json.Marshal(map[string]any{"id": "evt_pp_1", "attempt_id": a.ID,
		"type": "PAYMENT.CAPTURE.COMPLETED"})
	hdr := signPayPal(secret, string(payload), time.Now().UTC())
	rec := postPayPalWebhook(t, h, payload, hdr, true)
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
	if rec := postPayPalWebhook(t, h, payload, hdr, true); rec.Code != http.StatusOK {
		t.Fatalf("redelivery: code=%d", rec.Code)
	} else {
		var red struct {
			Duplicate bool `json:"duplicate"`
		}
		_ = json.NewDecoder(rec.Body).Decode(&red)
		if !red.Duplicate {
			t.Fatalf("redelivery not flagged: %s", rec.Body.String())
		}
	}
	// Bad signature → 401.
	bad := hdr
	bad.Signature = "deadbeef"
	if rec := postPayPalWebhook(t, h, payload, bad, true); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature: code=%d want 401", rec.Code)
	}
	// No tenant → 401 (never defaulted).
	if rec := postPayPalWebhook(t, h, payload, hdr, false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("tenantless: code=%d want 401", rec.Code)
	}
	// Unhandled type → 422.
	b := createAttemptViaAPI(t, h, "paypal", 10)
	weird, _ := json.Marshal(map[string]any{"id": "evt_pp_9", "attempt_id": b.ID, "type": "NOPE.NOPE"})
	if rec := postPayPalWebhook(t, h, weird, signPayPal(secret, string(weird), time.Now().UTC()), true); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unhandled type: code=%d want 422", rec.Code)
	}
	// Denied capture settles Failed.
	c := createAttemptViaAPI(t, h, "paypal", 10)
	denied, _ := json.Marshal(map[string]any{"id": "evt_pp_10", "attempt_id": c.ID, "type": "PAYMENT.CAPTURE.DENIED"})
	if rec := postPayPalWebhook(t, h, denied, signPayPal(secret, string(denied), time.Now().UTC()), true); rec.Code != http.StatusOK {
		t.Fatalf("denied: code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPayPalWebhookDisabled(t *testing.T) {
	h := liveRouter(Deps{Store: NewMemoryStore(), Providers: NewRegistry(),
		WebhookSecret: "whsec-test", Tolerance: 5 * time.Minute})
	rec := postPayPalWebhook(t, h, []byte(`{}`), PayPalWebhookHeaders{}, true)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled: code=%d want 503", rec.Code)
	}
}

func TestPayPalWebhookOrderFallback(t *testing.T) {
	// Live intent persists the order id as webhook key; a provider-native
	// event carrying only resource.id must still resolve the attempt.
	const secret = "whsec-pp"
	d := newPayPalDouble(t, "pp-id", "pp-secret")
	defer d.server.Close()
	reg := NewRegistry(NewOnlineProvider(ProviderPayPal))
	h := liveRouter(Deps{Store: NewMemoryStore(), Providers: reg,
		PayPal: NewPayPalClient(PayPalConfig{ClientID: "pp-id", ClientSecret: "pp-secret", BaseURL: d.server.URL}),
		PayPalWebhookSecret: secret, Tolerance: 5 * time.Minute})

	a := createAttemptViaAPI(t, h, "paypal", 2500)
	if a.Ref != "ORDER-1" || a.WebhookKey != "ORDER-1" {
		t.Fatalf("live order intent not keyed: %+v", a)
	}
	payload, _ := json.Marshal(map[string]any{"id": "evt_pp_native",
		"type":     "PAYMENT.CAPTURE.COMPLETED",
		"resource": map[string]string{"id": "ORDER-1"}})
	rec := postPayPalWebhook(t, h, payload, signPayPal(secret, string(payload), time.Now().UTC()), true)
	if rec.Code != http.StatusOK {
		t.Fatalf("native event: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var applied struct {
		Attempt PaymentAttempt `json:"attempt"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&applied)
	if applied.Attempt.Status != AttemptSucceeded {
		t.Fatalf("native event not applied: %s", rec.Body.String())
	}
}

func TestPayPalWebhookRemotePath(t *testing.T) {
	d := newPayPalDouble(t, "pp-id", "pp-secret")
	defer d.server.Close()
	reg := NewRegistry(NewOnlineProvider(ProviderPayPal))
	h := liveRouter(Deps{Store: NewMemoryStore(), Providers: reg,
		PayPal: NewPayPalClient(PayPalConfig{ClientID: "pp-id", ClientSecret: "pp-secret",
			WebhookID: "WH-1", BaseURL: d.server.URL}),
		Tolerance: 5 * time.Minute})

	a := createAttemptViaAPI(t, h, "paypal", 2500)
	payload, _ := json.Marshal(map[string]any{"id": "evt_pp_r", "attempt_id": a.ID,
		"type": "PAYMENT.CAPTURE.COMPLETED"})
	hdr := PayPalWebhookHeaders{TransmissionID: "tx-r", TransmissionTime: "2026-01-01T00:00:00Z", Signature: "remote"}
	if rec := postPayPalWebhook(t, h, payload, hdr, true); rec.Code != http.StatusOK {
		t.Fatalf("remote verify path: code=%d body=%s", rec.Code, rec.Body.String())
	}
	d.verifyMode = "FAILURE"
	b := createAttemptViaAPI(t, h, "paypal", 10)
	payload2, _ := json.Marshal(map[string]any{"id": "evt_pp_r2", "attempt_id": b.ID,
		"type": "PAYMENT.CAPTURE.COMPLETED"})
	if rec := postPayPalWebhook(t, h, payload2, hdr, true); rec.Code != http.StatusUnauthorized {
		t.Fatalf("remote failure: code=%d want 401", rec.Code)
	}
}

func TestCrossTenantRefundIsolation(t *testing.T) {
	// passthrough forces entity 1, so this test mounts with an
	// entity-preserving gate: preset contexts survive, unset ones get entity 1.
	preserve := func(_, _, _ string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := platform.EntityOf(r); err != nil {
					r = r.WithContext(platform.ContextWithEntity(r.Context(), 1))
				}
				next.ServeHTTP(w, r)
			})
		}
	}
	r := chi.NewRouter()
	d := Deps{Store: NewMemoryStore(), Providers: NewRegistry(), Tolerance: 5 * time.Minute}
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, d, preserve)
	})
	a := createAttemptViaAPI(t, r, "manual", 100)
	// Entity 2 must not see entity 1's attempt.
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/v1/payments/attempts/%d/refund", a.ID),
		bytes.NewReader([]byte(fmt.Sprintf(`{"row_version":%d}`, a.RowVersion))))
	req = req.WithContext(platform.ContextWithEntity(req.Context(), 2))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant refund: code=%d want 404", rec.Code)
	}
}
