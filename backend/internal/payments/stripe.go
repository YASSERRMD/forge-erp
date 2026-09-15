package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// defaultStripeAPI is the live Stripe endpoint. Tests override it with an
// httptest double via FERP_STRIPE_API_URL; production never sets that var.
const defaultStripeAPI = "https://api.stripe.com"

// StripeConfig carries live credentials. Empty SecretKey selects mint-only
// mode (no network): CreateIntent/Refund return offline references exactly
// like OnlineProvider, so unset secrets preserve historical behavior.
type StripeConfig struct {
	SecretKey  string // FERP_STRIPE_SECRET_KEY (live Bearer credential, never logged)
	BaseURL    string // FERP_STRIPE_API_URL override (tests); defaults to defaultStripeAPI
	HTTPClient *http.Client
}

// StripeConfigFromEnv reads FERP_STRIPE_SECRET_KEY (+ optional
// FERP_STRIPE_API_URL test override).
func StripeConfigFromEnv() StripeConfig {
	return StripeConfig{
		SecretKey: os.Getenv("FERP_STRIPE_SECRET_KEY"),
		BaseURL:   firstNonEmpty(os.Getenv("FERP_STRIPE_API_URL"), defaultStripeAPI),
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// StripeClient is the live Stripe provider: payment-intent creation and
// refunds over HTTP, mint-only fallback when SecretKey is unset.
type StripeClient struct {
	cfg StripeConfig
}

// NewStripeClient builds a Stripe provider client from cfg.
func NewStripeClient(cfg StripeConfig) *StripeClient {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = defaultStripeAPI
	}
	return &StripeClient{cfg: cfg}
}

// Name identifies the provider.
func (c *StripeClient) Name() string { return ProviderStripe }

// Live reports whether real provider calls will be made (false = mint-only).
func (c *StripeClient) Live() bool { return strings.TrimSpace(c.cfg.SecretKey) != "" }

func (c *StripeClient) httpClient() *http.Client {
	if c != nil && c.cfg.HTTPClient != nil {
		return c.cfg.HTTPClient
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// CreateIntent creates a Stripe payment intent (live) or mints an offline
// reference (mint-only). Amount is int64 minor units — Stripe's amount field
// uses the same unit, so no conversion (and no float) is involved.
func (c *StripeClient) CreateIntent(ctx context.Context, amount int64, currency string) (Intent, error) {
	if err := validateIntentInput(amount, currency); err != nil {
		return Intent{}, err
	}
	if !c.Live() {
		return Intent{Ref: mintIntentRef(ProviderStripe, currency)}, nil
	}
	form := url.Values{}
	form.Set("amount", fmt.Sprint(amount))
	form.Set("currency", strings.ToLower(strings.TrimSpace(currency)))
	form.Set("automatic_payment_methods[allow_redirects]", "never")
	var out struct {
		ID           string `json:"id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := c.postForm(ctx, "/v1/payment_intents", form, &out); err != nil {
		return Intent{}, err
	}
	if out.ID == "" {
		return Intent{}, errors.New("payments: stripe intent missing id")
	}
	return Intent{Ref: out.ID, ClientSecret: out.ClientSecret}, nil
}

// Refund issues a live Stripe refund against a payment intent, or mints an
// offline reference in mint-only mode (the handler still persists the
// Succeeded→Refunded transition either way).
func (c *StripeClient) Refund(ctx context.Context, providerRef string, amount int64) (string, error) {
	if providerRef == "" || amount <= 0 {
		return "", errors.New("payments: bad refund")
	}
	if !c.Live() {
		return mintRefundRef(ProviderStripe, providerRef), nil
	}
	form := url.Values{}
	form.Set("payment_intent", providerRef)
	form.Set("amount", fmt.Sprint(amount))
	var out struct {
		ID string `json:"id"`
	}
	if err := c.postForm(ctx, "/v1/refunds", form, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", errors.New("payments: stripe refund missing id")
	}
	return out.ID, nil
}

// postForm issues an authenticated form POST and decodes the JSON response
// into out. Errors carry status + a truncated body snippet; the secret is
// never included (it travels only in the Authorization header, which is never
// echoed into errors or logs).
func (c *StripeClient) postForm(ctx context.Context, path string, form url.Values, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(c.cfg.BaseURL, "/")+path, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("payments: stripe request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+c.cfg.SecretKey)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("payments: stripe call: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return fmt.Errorf("payments: stripe read: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return providerError(ProviderStripe, resp.StatusCode, raw)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("payments: stripe decode: %w", err)
	}
	return nil
}

// providerError renders a provider failure with a truncated body snippet.
// Provider error bodies are safe to quote (they never contain our secret);
// still truncated so logs stay bounded.
func providerError(provider string, status int, body []byte) error {
	snip := strings.TrimSpace(string(body))
	const maxSnip = 300
	if len(snip) > maxSnip {
		snip = snip[:maxSnip] + "…"
	}
	if snip == "" {
		snip = "empty body"
	}
	return fmt.Errorf("payments: %s call failed (status %d): %s", provider, status, snip)
}
