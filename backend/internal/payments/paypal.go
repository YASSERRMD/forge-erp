package payments

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// defaultPayPalAPI is the live PayPal endpoint. Tests override it with an
// httptest double via FERP_PAYPAL_API_URL; production points at
// https://api-m.paypal.com (or the sandbox host) via the same var.
const defaultPayPalAPI = "https://api-m.paypal.com"

// PayPalConfig carries live credentials. Empty ClientID/ClientSecret selects
// mint-only mode (no network): CreateIntent/Refund return offline references
// exactly like OnlineProvider, so unset secrets preserve historical behavior.
// WebhookSecret enables the offline HMAC webhook contract (test doubles);
// WebhookID enables live remote verification via the PayPal API.
type PayPalConfig struct {
	ClientID      string // FERP_PAYPAL_CLIENT_ID
	ClientSecret  string // FERP_PAYPAL_CLIENT_SECRET (Basic-auth credential, never logged)
	WebhookID     string // FERP_PAYPAL_WEBHOOK_ID (live remote verification)
	WebhookSecret string // FERP_PAYPAL_WEBHOOK_SECRET (HMAC test-double contract)
	BaseURL       string // FERP_PAYPAL_API_URL override; defaults to defaultPayPalAPI
	HTTPClient    *http.Client
}

// PayPalConfigFromEnv reads FERP_PAYPAL_* env.
func PayPalConfigFromEnv() PayPalConfig {
	return PayPalConfig{
		ClientID:      os.Getenv("FERP_PAYPAL_CLIENT_ID"),
		ClientSecret:  os.Getenv("FERP_PAYPAL_CLIENT_SECRET"),
		WebhookID:     os.Getenv("FERP_PAYPAL_WEBHOOK_ID"),
		WebhookSecret: os.Getenv("FERP_PAYPAL_WEBHOOK_SECRET"),
		BaseURL:       firstNonEmpty(os.Getenv("FERP_PAYPAL_API_URL"), defaultPayPalAPI),
	}
}

// PayPalClient is the live PayPal provider: order creation, capture and
// refunds over HTTP with a cached client-credentials token, mint-only
// fallback when credentials are unset.
type PayPalClient struct {
	cfg PayPalConfig

	mu        sync.Mutex
	access    string
	accessExp time.Time
}

// NewPayPalClient builds a PayPal provider client from cfg.
func NewPayPalClient(cfg PayPalConfig) *PayPalClient {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = defaultPayPalAPI
	}
	return &PayPalClient{cfg: cfg}
}

// Name identifies the provider.
func (c *PayPalClient) Name() string { return ProviderPayPal }

// Live reports whether real provider calls will be made (false = mint-only).
func (c *PayPalClient) Live() bool {
	return strings.TrimSpace(c.cfg.ClientID) != "" && strings.TrimSpace(c.cfg.ClientSecret) != ""
}

// RemoteVerify reports whether live API webhook verification is configured.
func (c *PayPalClient) RemoteVerify() bool { return c.Live() && strings.TrimSpace(c.cfg.WebhookID) != "" }

func (c *PayPalClient) httpClient() *http.Client {
	if c != nil && c.cfg.HTTPClient != nil {
		return c.cfg.HTTPClient
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// CreateIntent creates a PayPal order with intent CAPTURE (live) or mints an
// offline reference (mint-only). The approval URL is returned for the
// frontend redirect and is never persisted.
func (c *PayPalClient) CreateIntent(ctx context.Context, amount int64, currency string) (Intent, error) {
	if err := validateIntentInput(amount, currency); err != nil {
		return Intent{}, err
	}
	if !c.Live() {
		return Intent{Ref: mintIntentRef(ProviderPayPal, currency)}, nil
	}
	value, err := paypalAmount(currency, amount)
	if err != nil {
		return Intent{}, err
	}
	body, _ := json.Marshal(map[string]any{
		"intent": "CAPTURE",
		"purchase_units": []map[string]any{
			{"amount": map[string]string{
				"currency_code": strings.ToUpper(strings.TrimSpace(currency)),
				"value":         value,
			}},
		},
	})
	var out struct {
		ID    string `json:"id"`
		Links []struct {
			Href string `json:"href"`
			Rel  string `json:"rel"`
		} `json:"links"`
	}
	if err := c.postJSON(ctx, "/v2/checkout/orders", body, &out); err != nil {
		return Intent{}, err
	}
	if out.ID == "" {
		return Intent{}, errors.New("payments: paypal order missing id")
	}
	return Intent{Ref: out.ID, ApprovalURL: paypalApprovalURL(out.Links)}, nil
}

// Capture captures an approved PayPal order and returns the capture id.
// Capture is a live-only operation (no meaningful offline equivalent).
func (c *PayPalClient) Capture(ctx context.Context, orderID string) (string, error) {
	if strings.TrimSpace(orderID) == "" {
		return "", errors.New("payments: bad capture order")
	}
	if !c.Live() {
		return "", errors.New("payments: paypal capture not configured")
	}
	var out struct {
		PurchaseUnits []struct {
			Payments struct {
				Captures []struct {
					ID string `json:"id"`
				} `json:"captures"`
			} `json:"payments"`
		} `json:"purchase_units"`
	}
	if err := c.postJSON(ctx, "/v2/checkout/orders/"+orderID+"/capture", []byte(`{}`), &out); err != nil {
		return "", err
	}
	for _, u := range out.PurchaseUnits {
		for _, cp := range u.Payments.Captures {
			if cp.ID != "" {
				return cp.ID, nil
			}
		}
	}
	return "", errors.New("payments: paypal capture missing id")
}

// Refund refunds a captured PayPal payment. Live mode resolves the order to
// its first capture id (GET order) and then refunds that capture; mint-only
// mode returns an offline reference (the handler still persists the
// Succeeded→Refunded transition either way).
func (c *PayPalClient) Refund(ctx context.Context, providerRef string, amount int64) (string, error) {
	if providerRef == "" || amount <= 0 {
		return "", errors.New("payments: bad refund")
	}
	if !c.Live() {
		return mintRefundRef(ProviderPayPal, providerRef), nil
	}
	captureID, currency, err := c.firstCapture(ctx, providerRef)
	if err != nil {
		return "", err
	}
	value, err := paypalAmount(currency, amount)
	if err != nil {
		return "", err
	}
	body, _ := json.Marshal(map[string]any{
		"amount": map[string]string{"currency_code": currency, "value": value},
	})
	var out struct {
		ID string `json:"id"`
	}
	if err := c.postJSON(ctx, "/v2/payments/captures/"+captureID+"/refund", body, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", errors.New("payments: paypal refund missing id")
	}
	return out.ID, nil
}

// firstCapture resolves an order to its first capture id + currency.
func (c *PayPalClient) firstCapture(ctx context.Context, orderID string) (captureID, currency string, err error) {
	var out struct {
		PurchaseUnits []struct {
			Amount struct {
				CurrencyCode string `json:"currency_code"`
			} `json:"amount"`
			Payments struct {
				Captures []struct {
					ID string `json:"id"`
				} `json:"captures"`
			} `json:"payments"`
		} `json:"purchase_units"`
	}
	if err := c.getJSON(ctx, "/v2/checkout/orders/"+orderID, &out); err != nil {
		return "", "", err
	}
	for _, u := range out.PurchaseUnits {
		for _, cp := range u.Payments.Captures {
			if cp.ID != "" {
				return cp.ID, u.Amount.CurrencyCode, nil
			}
		}
	}
	return "", "", errors.New("payments: paypal order has no capture to refund")
}

// accessToken returns a cached client-credentials token, refreshing it when
// missing or within 60s of expiry. The secret travels only in the Basic-auth
// header and is never echoed into errors or logs.
func (c *PayPalClient) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.access != "" && time.Now().UTC().Add(60*time.Second).Before(c.accessExp) {
		tok := c.access
		c.mu.Unlock()
		return tok, nil
	}
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(c.cfg.BaseURL, "/")+"/v1/oauth2/token",
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("payments: paypal token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(c.cfg.ClientID, c.cfg.ClientSecret)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("payments: paypal token call: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", fmt.Errorf("payments: paypal token read: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", providerError(ProviderPayPal, resp.StatusCode, raw)
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("payments: paypal token decode: %w", err)
	}
	if out.AccessToken == "" {
		return "", errors.New("payments: paypal token missing access_token")
	}
	exp := time.Now().UTC().Add(time.Duration(out.ExpiresIn) * time.Second)
	if out.ExpiresIn <= 0 {
		exp = time.Now().UTC().Add(5 * time.Minute)
	}
	c.mu.Lock()
	c.access, c.accessExp = out.AccessToken, exp
	c.mu.Unlock()
	return out.AccessToken, nil
}

// postJSON issues an authenticated JSON POST and decodes the response.
func (c *PayPalClient) postJSON(ctx context.Context, path string, body []byte, out any) error {
	return c.doJSON(ctx, http.MethodPost, path, body, out)
}

// getJSON issues an authenticated GET and decodes the response.
func (c *PayPalClient) getJSON(ctx context.Context, path string, out any) error {
	return c.doJSON(ctx, http.MethodGet, path, nil, out)
}

func (c *PayPalClient) doJSON(ctx context.Context, method, path string, body []byte, out any) error {
	tok, err := c.accessToken(ctx)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.cfg.BaseURL, "/")+path, rdr)
	if err != nil {
		return fmt.Errorf("payments: paypal request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	// Never log tok or the client secret; only the redacted error below leaves this client.
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("payments: paypal call: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return fmt.Errorf("payments: paypal read: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return providerError(ProviderPayPal, resp.StatusCode, raw)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("payments: paypal decode: %w", err)
	}
	return nil
}

// paypalApprovalURL picks the payer-approval link ("approve", falling back to
// the deprecated "payer-action" rel) from an order response.
func paypalApprovalURL(links []struct {
	Href string `json:"href"`
	Rel  string `json:"rel"`
}) string {
	fallback := ""
	for _, l := range links {
		switch l.Rel {
		case "approve":
			return l.Href
		case "payer-action":
			fallback = l.Href
		}
	}
	return fallback
}

// paypalZeroDecimal lists ISO-4217 codes with no minor unit (PayPal value is
// whole units). Everything else formats with exactly two decimals. Math is
// integer-only — no floats anywhere near money.
func paypalZeroDecimal(code string) bool {
	switch code {
	case "BIF", "CLP", "DJF", "GNF", "JPY", "KMF", "KRW", "MGA",
		"PYG", "RWF", "UGX", "UYI", "VND", "VUV", "XAF", "XOF", "XPF":
		return true
	}
	return false
}

// paypalAmount renders int64 minor units as a PayPal major-units value string.
func paypalAmount(currency string, minor int64) (string, error) {
	if minor <= 0 {
		return "", errors.New("payments: bad intent")
	}
	code := strings.ToUpper(strings.TrimSpace(currency))
	if code == "" {
		return "", errors.New("payments: bad intent")
	}
	if paypalZeroDecimal(code) {
		return strconv.FormatInt(minor, 10), nil
	}
	return fmt.Sprintf("%d.%02d", minor/100, minor%100), nil
}

// PayPalWebhookHeaders carries the PayPal transmission headers that
// authenticate a webhook delivery.
type PayPalWebhookHeaders struct {
	TransmissionID   string // PAYPAL-TRANSMISSION-ID
	TransmissionTime string // PAYPAL-TRANSMISSION-TIME (RFC3339, unix fallback)
	Signature        string // PAYPAL-TRANSMISSION-SIG (hex HMAC-SHA256)
}

// paypalHeadersFromRequest extracts transmission headers from r.
func paypalHeadersFromRequest(r *http.Request) PayPalWebhookHeaders {
	return PayPalWebhookHeaders{
		TransmissionID:   r.Header.Get("PAYPAL-TRANSMISSION-ID"),
		TransmissionTime: r.Header.Get("PAYPAL-TRANSMISSION-TIME"),
		Signature:        r.Header.Get("PAYPAL-TRANSMISSION-SIG"),
	}
}

// VerifyPayPalSignature checks the offline HMAC webhook contract: hex
// HMAC-SHA256 of "<transmission-id>.<transmission-time>.<payload>" under the
// webhook secret, with timestamp tolerance. Comparison is constant-time
// (hmac.Equal). This is the test-double contract (verified against httptest
// doubles); live deployments should prefer VerifyWebhookRemote when
// FERP_PAYPAL_WEBHOOK_ID is configured (see handler precedence).
func VerifyPayPalSignature(secret string, h PayPalWebhookHeaders, payload string, tolerance time.Duration, now time.Time) error {
	if strings.TrimSpace(secret) == "" {
		return errors.New("payments: paypal webhook not configured")
	}
	if h.TransmissionID == "" || h.TransmissionTime == "" || h.Signature == "" {
		return errors.New("payments: malformed paypal webhook headers")
	}
	ts, err := parsePayPalTime(h.TransmissionTime)
	if err != nil {
		return errors.New("payments: bad paypal webhook timestamp")
	}
	if skew := now.Unix() - ts; skew < 0 || time.Duration(skew)*time.Second > tolerance {
		return errors.New("payments: paypal webhook timestamp outside tolerance")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%s.%s.%s", h.TransmissionID, h.TransmissionTime, payload)
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(strings.ToLower(h.Signature)), []byte(want)) {
		return errors.New("payments: paypal webhook signature mismatch")
	}
	return nil
}

// parsePayPalTime accepts PayPal transmission timestamps (RFC3339, with unix
// seconds fallback for test doubles).
func parsePayPalTime(s string) (int64, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Unix(), nil
	}
	return strconv.ParseInt(strings.TrimSpace(s), 10, 64)
}

// VerifyWebhookRemote verifies a webhook delivery against the live PayPal
// verify-webhook-signature API (requires WebhookID + live credentials).
// The request carries only transmission metadata and the raw event; the
// client secret travels solely in the token call's Basic-auth header.
func (c *PayPalClient) VerifyWebhookRemote(ctx context.Context, h PayPalWebhookHeaders, body []byte) error {
	if !c.RemoteVerify() {
		return errors.New("payments: paypal remote verify not configured")
	}
	if h.TransmissionID == "" || h.TransmissionTime == "" || h.Signature == "" {
		return errors.New("payments: malformed paypal webhook headers")
	}
	var event json.RawMessage
	if err := json.Unmarshal(body, &event); err != nil {
		return errors.New("payments: bad paypal webhook body")
	}
	reqBody, _ := json.Marshal(map[string]any{
		"transmission_id":   h.TransmissionID,
		"transmission_time": h.TransmissionTime,
		"transmission_sig":  h.Signature,
		"webhook_id":        c.cfg.WebhookID,
		"webhook_event":     event,
	})
	var out struct {
		VerificationStatus string `json:"verification_status"`
	}
	if err := c.postJSON(ctx, "/v1/notifications/verify-webhook-signature", reqBody, &out); err != nil {
		return err
	}
	if !strings.EqualFold(out.VerificationStatus, "SUCCESS") {
		return errors.New("payments: paypal webhook not verified")
	}
	return nil
}
