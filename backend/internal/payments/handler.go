package payments

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Deps wires handlers to persistence, providers, webhook secrets and the bus.
// Stripe / PayPal are optional live clients: when nil (or not Live, i.e.
// secrets unset) intent/refund calls degrade to the registry's mint-only
// providers, preserving historical offline behavior. WebhookSecret (Stripe)
// and PayPalWebhookSecret (HMAC contract) empty = that webhook disabled (503).
type Deps struct {
	Store               Store
	DB                  platform.DBTX
	Providers           *Registry
	WebhookSecret       string
	PayPalWebhookSecret string
	Stripe              *StripeClient
	PayPal              *PayPalClient
	Tolerance           time.Duration
	Bus                 platform.Bus
}

// WebhookSecretFromEnv reads FERP_STRIPE_WEBHOOK_SECRET (empty = endpoint disabled).
func WebhookSecretFromEnv() string { return os.Getenv("FERP_STRIPE_WEBHOOK_SECRET") }

// PayPalWebhookSecretFromEnv reads FERP_PAYPAL_WEBHOOK_SECRET (empty = HMAC
// webhook disabled; live remote verification via FERP_PAYPAL_WEBHOOK_ID still
// applies when the PayPal client is configured — see PayPalWebhook).
func PayPalWebhookSecretFromEnv() string { return os.Getenv("FERP_PAYPAL_WEBHOOK_SECRET") }

// Middleware builds Require-style RBAC gates (identity.Handler.Require in production).
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the payments surface (caller nests at /api/v1). Both
// webhooks are intentionally unauthenticated (HMAC / PayPal verification is
// the credential).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	if h.deps.Tolerance <= 0 {
		h.deps.Tolerance = 5 * time.Minute
	}
	r.With(mw("payments", "attempt", "write")).Post("/payments/intents", h.CreateIntent)
	r.With(mw("payments", "attempt", "read")).Get("/payments/attempts", h.ListAttempts)
	r.With(mw("payments", "attempt", "validate")).Post("/payments/attempts/{id}/status", h.SetAttemptStatus)
	r.With(mw("payments", "attempt", "validate")).Post("/payments/attempts/{id}/refund", h.Refund)
	r.Post("/payments/webhooks/stripe", h.StripeWebhook)
	r.Post("/payments/webhooks/paypal", h.PayPalWebhook)
}

// Handler implements the payments HTTP surface.
type Handler struct{ deps Deps }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func decode(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v)
}

func pathID(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func (h *Handler) publish(ctx context.Context, entityID int64, subject, entity string, id int64) {
	if h.deps.Bus == nil {
		return
	}
	_ = h.deps.Bus.Publish(ctx, platform.Event{Subject: subject, Entity: entity, EntityID: entityID, ID: id})
}

type intentIn struct {
	OrgID     int64  `json:"org_id"`
	InvoiceID *int64 `json:"invoice_id"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
	Provider  string `json:"provider"`
}

// CreateIntent starts a collection: manual settles immediately, online
// providers return a client reference for the frontend to confirm. When the
// matching live client is configured (Stripe/PayPal secrets set), the intent
// is created at the provider and its ref doubles as the webhook key so
// provider-native events can resolve the attempt without our id in band;
// otherwise the registry mints an offline reference (historical behavior).
func (h *Handler) CreateIntent(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var in intentIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	prov, err := h.deps.Providers.Resolve(in.Provider)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	intent, live, err := h.createIntent(r.Context(), prov, in.Provider, in.Amount, in.Currency)
	if err != nil {
		// Intent-creation failures (bad input or live provider rejection)
		// stay 422 on the historical contract; provider error text is safe
		// to quote (secrets never leave the clients — see stripe.go/paypal.go).
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	a := &PaymentAttempt{EntityID: entityID, Ref: intent.Ref, OrgID: in.OrgID,
		InvoiceID: in.InvoiceID, Amount: in.Amount, Currency: in.Currency,
		Provider: in.Provider, Status: AttemptPending}
	if live {
		a.WebhookKey = intent.Ref
	}
	if err := a.Validate(); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := h.deps.Store.CreateAttempt(r.Context(), h.deps.DB, a); err != nil {
		platform.WriteError(w, err)
		return
	}
	if in.Provider == ProviderManual {
		settled, err := h.deps.Store.SetAttemptStatus(r.Context(), h.deps.DB, entityID, a.ID, AttemptSucceeded, a.RowVersion)
		if err != nil {
			platform.WriteError(w, err)
			return
		}
		*a = settled
	}
	h.publish(r.Context(), entityID, "forgeerp.payments.attempt.created.v1", "attempt", a.ID)
	resp := map[string]any{"attempt": a, "client_ref": intent.Ref}
	if intent.ClientSecret != "" {
		resp["client_secret"] = intent.ClientSecret
	}
	if intent.ApprovalURL != "" {
		resp["approval_url"] = intent.ApprovalURL
	}
	writeJSON(w, http.StatusCreated, resp)
}

// createIntent prefers the configured live client for stripe/paypal and
// falls back to the registry (mint-only) provider. It reports whether the
// ref is provider-live (persisted as webhook key for event resolution).
func (h *Handler) createIntent(ctx context.Context, prov Provider, name string, amount int64, currency string) (Intent, bool, error) {
	if client, ok := h.liveClientFor(name); ok {
		intent, err := client.CreateIntent(ctx, amount, currency)
		return intent, true, err
	}
	intent, err := prov.CreateIntent(ctx, amount, currency)
	return intent, false, err
}

// liveClientFor returns the configured live client for name, if any.
func (h *Handler) liveClientFor(name string) (Provider, bool) {
	switch name {
	case ProviderStripe:
		if h.deps.Stripe != nil && h.deps.Stripe.Live() {
			return h.deps.Stripe, true
		}
	case ProviderPayPal:
		if h.deps.PayPal != nil && h.deps.PayPal.Live() {
			return h.deps.PayPal, true
		}
	}
	return nil, false
}

// ListAttempts pages attempts within the caller's entity.
func (h *Handler) ListAttempts(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	list, err := h.deps.Store.ListAttempts(r.Context(), h.deps.DB, entityID, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type statusIn struct {
	Status     int16 `json:"status"`
	RowVersion int64 `json:"row_version"`
}

// SetAttemptStatus settles or refunds an attempt (manual/refund ops).
func (h *Handler) SetAttemptStatus(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var in statusIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	a, err := h.deps.Store.SetAttemptStatus(r.Context(), h.deps.DB, entityID, id, AttemptStatus(in.Status), in.RowVersion)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

type stripeEvent struct {
	ID        string `json:"id"`         // provider event id (idempotency scope)
	AttemptID int64  `json:"attempt_id"` // our attempt
	Type      string `json:"type"`       // payment_intent.succeeded|payment_intent.payment_failed
}

type refundIn struct {
	RowVersion int64  `json:"row_version"`
	Amount     *int64 `json:"amount,omitempty"` // partial (minor units); defaults to the full amount
}

// Refund refunds a settled attempt. With a live client configured the refund
// is issued at the provider first (refund_ref + live:true); otherwise the
// refund is recorded mint-only (live:false) exactly like the historical manual
// status transition. Repeating a refund returns {duplicate:true} instead of
// re-transitioning. There is no partial-refund state: any positive amount up
// to the attempt total moves the attempt to Refunded.
func (h *Handler) Refund(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var in refundIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	a, err := h.deps.Store.AttemptByID(r.Context(), h.deps.DB, entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	if a.Status == AttemptRefunded {
		writeJSON(w, http.StatusOK, map[string]any{"duplicate": true, "attempt": a, "live": false})
		return
	}
	if a.Status != AttemptSucceeded {
		platform.WriteError(w, platform.ErrValidation)
		return
	}
	amount := a.Amount
	if in.Amount != nil {
		if *in.Amount <= 0 || *in.Amount > a.Amount {
			platform.WriteError(w, platform.ErrValidation)
			return
		}
		amount = *in.Amount
	}
	live := false
	refundRef := ""
	if client, isLive := h.liveClientFor(a.Provider); isLive {
		refundRef, err = client.Refund(r.Context(), a.Ref, amount)
		if err != nil {
			// Live provider failure: WriteError renders generic 500 and
			// logs the detail server-side; the secret never leaves the client.
			platform.WriteError(w, err)
			return
		}
		live = true
	} else {
		prov, err := h.deps.Providers.Resolve(a.Provider)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		refundRef, err = prov.Refund(r.Context(), a.Ref, amount)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
	}
	upd, err := h.deps.Store.SetAttemptStatus(r.Context(), h.deps.DB, entityID, a.ID, AttemptRefunded, in.RowVersion)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.payments.attempt.refunded.v1", "attempt", upd.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"duplicate": false, "attempt": upd, "live": live, "refund_ref": refundRef,
	})
}

// settleTerminal applies a terminal webhook transition idempotently: a
// non-pending attempt means redelivery → (attempt, true, nil) instead of
// re-transitioning.
func (h *Handler) settleTerminal(ctx context.Context, entityID int64, a PaymentAttempt, to AttemptStatus) (PaymentAttempt, bool, error) {
	if a.Status != AttemptPending {
		return a, true, nil
	}
	upd, err := h.deps.Store.SetAttemptStatus(ctx, h.deps.DB, entityID, a.ID, to, a.RowVersion)
	if err != nil {
		return PaymentAttempt{}, false, err
	}
	return upd, false, nil
}

// StripeWebhook applies a verified provider event idempotently: redelivery of
// StripeWebhook applies a verified provider event idempotently: redelivery of
// a terminal attempt returns {duplicate:true} instead of re-transitioning.
// The endpoint is intentionally unauthenticated (HMAC is the credential), but
// the tenant is still never defaulted: callers without a resolvable entity
// get 401. The edge that terminates Stripe traffic must resolve the tenant
// (see main.go wiring).
func (h *Handler) StripeWebhook(w http.ResponseWriter, r *http.Request) {
	if h.deps.WebhookSecret == "" {
		writeErr(w, http.StatusServiceUnavailable, "payments: webhook not configured")
		return
	}
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	if err := VerifyStripeSignature(h.deps.WebhookSecret, string(raw),
		r.Header.Get("Stripe-Signature"), h.deps.Tolerance, time.Now().UTC()); err != nil {
		writeErr(w, http.StatusUnauthorized, err.Error())
		return
	}
	var ev stripeEvent
	if err := json.Unmarshal(raw, &ev); err != nil || ev.AttemptID <= 0 || ev.ID == "" {
		writeErr(w, http.StatusBadRequest, "bad event")
		return
	}
	a, err := h.deps.Store.AttemptByID(r.Context(), h.deps.DB, entityID, ev.AttemptID)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	var to AttemptStatus
	switch ev.Type {
	case "payment_intent.succeeded":
		to = AttemptSucceeded
	case "payment_intent.payment_failed":
		to = AttemptFailed
	default:
		writeErr(w, http.StatusUnprocessableEntity, "payments: unhandled event type")
		return
	}
	upd, duplicate, err := h.settleTerminal(r.Context(), entityID, a, to)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	if !duplicate {
		h.publish(r.Context(), entityID, "forgeerp.payments.attempt."+strings.ToLower(ev.Type)+".v1", "attempt", upd.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"duplicate": duplicate, "attempt": upd})
}

type paypalEvent struct {
	ID        string `json:"id"`         // provider event id (idempotency scope)
	AttemptID int64  `json:"attempt_id"` // our attempt (test-double contract)
	Type      string `json:"type"`       // PAYMENT.CAPTURE.COMPLETED|...DENIED|...REFUNDED
	Resource  struct {
		ID string `json:"id"` // provider order id; resolves via webhook key for live intents
	} `json:"resource"`
}

// PayPalWebhook applies a verified provider event idempotently, mirroring
// StripeWebhook. Verification prefers the live PayPal verify API when
// FERP_PAYPAL_WEBHOOK_ID is configured, else the HMAC test-double contract
// under FERP_PAYPAL_WEBHOOK_SECRET; with neither configured the endpoint is
// 503. The tenant is never defaulted: requests without a resolvable entity
// get 401. Attempt resolution prefers our attempt_id, falling back to the
// provider order id (live intents persist their ref as webhook key).
func (h *Handler) PayPalWebhook(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	hdr := paypalHeadersFromRequest(r)
	switch {
	case h.deps.PayPal != nil && h.deps.PayPal.RemoteVerify():
		if err := h.deps.PayPal.VerifyWebhookRemote(r.Context(), hdr, raw); err != nil {
			writeErr(w, http.StatusUnauthorized, err.Error())
			return
		}
	case h.deps.PayPalWebhookSecret != "":
		if err := VerifyPayPalSignature(h.deps.PayPalWebhookSecret, hdr, string(raw),
			h.deps.Tolerance, time.Now().UTC()); err != nil {
			writeErr(w, http.StatusUnauthorized, err.Error())
			return
		}
	default:
		writeErr(w, http.StatusServiceUnavailable, "payments: paypal webhook not configured")
		return
	}
	// Verified signature is the credential, but tenant resolution still
	// applies — never default the entity.
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var ev paypalEvent
	if err := json.Unmarshal(raw, &ev); err != nil || ev.ID == "" {
		writeErr(w, http.StatusBadRequest, "bad event")
		return
	}
	var a PaymentAttempt
	switch {
	case ev.AttemptID > 0:
		a, err = h.deps.Store.AttemptByID(r.Context(), h.deps.DB, entityID, ev.AttemptID)
		if err != nil {
			platform.WriteError(w, err)
			return
		}
	case ev.Resource.ID != "":
		var found bool
		a, found = h.deps.Store.AttemptByWebhook(r.Context(), h.deps.DB, entityID, ev.Resource.ID)
		if !found {
			platform.WriteError(w, platform.ErrNotFound)
			return
		}
	default:
		writeErr(w, http.StatusBadRequest, "bad event")
		return
	}
	var to AttemptStatus
	switch ev.Type {
	case "PAYMENT.CAPTURE.COMPLETED":
		to = AttemptSucceeded
	case "PAYMENT.CAPTURE.DENIED", "PAYMENT.CAPTURE.FAILED", "CHECKOUT.ORDER.VOIDED":
		to = AttemptFailed
	case "PAYMENT.CAPTURE.REFUNDED", "PAYMENT.CAPTURE.REVERSED":
		to = AttemptRefunded
	default:
		writeErr(w, http.StatusUnprocessableEntity, "payments: unhandled event type")
		return
	}
	// REFUNDED is only legal from Succeeded (CanTransition): a pending
	// attempt receiving a refund event surfaces as 422 via WriteError below,
	// which is the honest signal (capture must precede the refund).
	upd, duplicate, err := h.settleTerminal(r.Context(), entityID, a, to)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	if !duplicate {
		h.publish(r.Context(), entityID, "forgeerp.payments.attempt."+strings.ToLower(ev.Type)+".v1", "attempt", upd.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"duplicate": duplicate, "attempt": upd})
}
