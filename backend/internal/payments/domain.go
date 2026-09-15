// Package payments implements online/offline payment provider integration
// (Dolibarr paypal/stripe modules): a provider registry with live HTTP
// clients (StripeClient, PayPalClient) that degrade to mint-only references
// when secrets are unset, recorded payment attempts with webhook idempotency,
// and HMAC webhook verification for both providers. Live provider secrets
// stay in env (FERP_STRIPE_*, FERP_PAYPAL_*); raw secrets are never logged or
// persisted (only provider refs and redacted error snippets leave the client).
// All logic below is verifiable offline with httptest doubles + test vectors.
package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Attempt status.
type AttemptStatus int16

const (
	AttemptPending   AttemptStatus = 0
	AttemptSucceeded AttemptStatus = 1
	AttemptFailed    AttemptStatus = -1
	AttemptRefunded  AttemptStatus = -2
)

// Providers.
const (
	ProviderManual = "manual"
	ProviderStripe = "stripe"
	ProviderPayPal = "paypal"
)

// PaymentAttempt records one collection try (idempotent on webhook key).
type PaymentAttempt struct {
	ID          int64         `json:"id"`
	EntityID    int64         `json:"entity_id"`
	Ref         string        `json:"ref"` // unique per entity
	OrgID       int64         `json:"org_id"`
	InvoiceID   *int64        `json:"invoice_id"`
	Amount      int64         `json:"amount"` // minor units, > 0
	Currency    string        `json:"currency"`
	Provider    string        `json:"provider"`
	Status      AttemptStatus `json:"status"`
	WebhookKey  string        `json:"webhook_key"` // idempotency key from provider, unique when set
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	RowVersion  int64         `json:"row_version"`
}

// Validate checks attempt invariants.
func (a PaymentAttempt) Validate() error {
	if a.EntityID <= 0 {
		return errors.New("payments: entity_id required")
	}
	if strings.TrimSpace(a.Ref) == "" {
		return errors.New("payments: ref required")
	}
	if a.OrgID <= 0 {
		return errors.New("payments: org_id required")
	}
	if a.Amount <= 0 {
		return errors.New("payments: amount must be positive")
	}
	if strings.TrimSpace(a.Currency) == "" {
		return errors.New("payments: currency required")
	}
	switch a.Provider {
	case ProviderManual, ProviderStripe, ProviderPayPal:
	default:
		return fmt.Errorf("payments: unknown provider %q", a.Provider)
	}
	return nil
}

// CanTransition reports whether an attempt status change is legal.
func (a PaymentAttempt) CanTransition(to AttemptStatus) bool {
	switch a.Status {
	case AttemptPending:
		return to == AttemptSucceeded || to == AttemptFailed
	case AttemptSucceeded:
		return to == AttemptRefunded
	default:
		return false
	}
}

// Provider creates collection intents and issues refunds. Live clients
// (StripeClient, PayPalClient) perform real provider HTTP calls when their
// secrets are configured and fall back to mint-only references otherwise, so
// unset secrets preserve the historical offline behavior. Manual settles
// immediately; online providers return a client secret / approval URL for
// the frontend. Amounts are int64 minor units (> 0); currency is ISO-4217.
type Provider interface {
	Name() string
	CreateIntent(ctx context.Context, amount int64, currency string) (Intent, error)
	Refund(ctx context.Context, providerRef string, amount int64) (refundRef string, err error)
}

// Intent is a provider-side collection reference. Ref is persisted as
// PaymentAttempt.Ref; ClientSecret / ApprovalURL are returned to the frontend
// and are NEVER persisted (they carry confirmation capability, not identity).
type Intent struct {
	Ref          string
	ClientSecret string // Stripe payment-intent client_secret (empty for others)
	ApprovalURL  string // PayPal payer-approval link (empty for others)
}

// validateIntentInput guards all intent/refund entry points (minor units, ISO code).
func validateIntentInput(amount int64, currency string) error {
	if amount <= 0 || strings.TrimSpace(currency) == "" {
		return errors.New("payments: bad intent")
	}
	return nil
}

// mintIntentRef mints a provider-side intent reference without network calls
// (offline/test-mode behavior shared by OnlineProvider and live clients whose
// secrets are unset).
func mintIntentRef(name, currency string) string {
	return fmt.Sprintf("%s-pi-%d-%s", name, time.Now().UTC().UnixNano(), strings.ToUpper(currency))
}

// mintRefundRef mints an offline refund reference (same no-network contract).
func mintRefundRef(name, providerRef string) string {
	return fmt.Sprintf("%s-re-%s-%d", name, providerRef, time.Now().UTC().UnixNano())
}

// ManualProvider settles offline collections (cash/check at the counter).
type ManualProvider struct{}

// Name identifies the provider.
func (ManualProvider) Name() string { return ProviderManual }

// CreateIntent records an offline intent reference.
func (ManualProvider) CreateIntent(_ context.Context, amount int64, currency string) (Intent, error) {
	if err := validateIntentInput(amount, currency); err != nil {
		return Intent{}, err
	}
	return Intent{Ref: fmt.Sprintf("manual-%d-%s", time.Now().UTC().UnixNano(), strings.ToUpper(currency))}, nil
}

// Refund records an offline refund (no network; the handler persists the
// Succeeded→Refunded transition).
func (ManualProvider) Refund(_ context.Context, providerRef string, amount int64) (string, error) {
	if providerRef == "" || amount <= 0 {
		return "", errors.New("payments: bad refund")
	}
	return mintRefundRef(ProviderManual, providerRef), nil
}

// OnlineProvider is a config-driven placeholder for Stripe/PayPal: it mints
// provider-side intent references without network calls. Prefer StripeClient /
// PayPalClient (live when FERP_STRIPE_*/FERP_PAYPAL_* secrets are configured,
// mint-only otherwise); settlement always arrives via verified webhooks regardless.
type OnlineProvider struct {
	name string
}

// NewOnlineProvider builds a named online provider (stripe|paypal).
func NewOnlineProvider(name string) OnlineProvider { return OnlineProvider{name: name} }

// Name identifies the provider.
func (p OnlineProvider) Name() string { return p.name }

// CreateIntent mints a provider-side intent reference.
func (p OnlineProvider) CreateIntent(_ context.Context, amount int64, currency string) (Intent, error) {
	if err := validateIntentInput(amount, currency); err != nil {
		return Intent{}, err
	}
	return Intent{Ref: mintIntentRef(p.name, currency)}, nil
}

// Refund mints an offline refund reference (no network; the handler persists
// the Succeeded→Refunded transition).
func (p OnlineProvider) Refund(_ context.Context, providerRef string, amount int64) (string, error) {
	if providerRef == "" || amount <= 0 {
		return "", errors.New("payments: bad refund")
	}
	return mintRefundRef(p.name, providerRef), nil
}
// Registry resolves providers by name.
type Registry struct {
	providers map[string]Provider
}

// NewRegistry builds a registry with the manual provider always present.
func NewRegistry(extra ...Provider) *Registry {
	r := &Registry{providers: map[string]Provider{}}
	r.Register(ManualProvider{})
	for _, p := range extra {
		r.Register(p)
	}
	return r
}

// Register adds a provider (overwrites same-name entries).
func (r *Registry) Register(p Provider) { r.providers[p.Name()] = p }

// Resolve returns a provider or an error for unknown names.
func (r *Registry) Resolve(name string) (Provider, error) {
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("payments: unknown provider %q", name)
	}
	return p, nil
}

// VerifyStripeSignature checks a Stripe-style webhook signature header
// ("t=<unix>,v1=<hex hmac-sha256 of '<t>.<payload>' with tolerance").
// Comparison is constant-time (hmac.Equal); fully offline-testable — live
// mode only needs FERP_STRIPE_WEBHOOK_SECRET.
func VerifyStripeSignature(secret, payload, header string, tolerance time.Duration, now time.Time) error {
	var ts int64 = -1
	var sigs []string
	for _, part := range strings.Split(header, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		if kv[0] == "t" {
			v, err := strconv.ParseInt(kv[1], 10, 64)
			if err != nil {
				return errors.New("payments: bad webhook timestamp")
			}
			ts = v
		}
		if kv[0] == "v1" {
			sigs = append(sigs, kv[1])
		}
	}
	if ts < 0 || len(sigs) == 0 {
		return errors.New("payments: malformed webhook signature")
	}
	if skew := now.Unix() - ts; skew < 0 || time.Duration(skew)*time.Second > tolerance {
		return errors.New("payments: webhook timestamp outside tolerance")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d.%s", ts, payload)
	want := hex.EncodeToString(mac.Sum(nil))
	for _, s := range sigs {
		if hmac.Equal([]byte(s), []byte(want)) {
			return nil
		}
	}
	return errors.New("payments: webhook signature mismatch")
}
