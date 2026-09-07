package payments

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Deps wires handlers to persistence, providers, webhook secret and the bus.
type Deps struct {
	Store         Store
	Providers     *Registry
	WebhookSecret string
	Tolerance     time.Duration
	Bus           platform.Bus
}

// WebhookSecretFromEnv reads FERP_STRIPE_WEBHOOK_SECRET (empty = endpoint disabled).
func WebhookSecretFromEnv() string { return os.Getenv("FERP_STRIPE_WEBHOOK_SECRET") }

// Middleware builds Require-style RBAC gates (identity.Handler.Require in production).
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the payments surface (caller nests at /api/v1). The Stripe
// webhook is intentionally unauthenticated (HMAC is the credential).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	if h.deps.Tolerance <= 0 {
		h.deps.Tolerance = 5 * time.Minute
	}
	r.With(mw("payments", "attempt", "write")).Post("/payments/intents", h.CreateIntent)
	r.With(mw("payments", "attempt", "read")).Get("/payments/attempts", h.ListAttempts)
	r.With(mw("payments", "attempt", "validate")).Post("/payments/attempts/{id}/status", h.SetAttemptStatus)
	r.Post("/payments/webhooks/stripe", h.StripeWebhook)
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

func entityOf(r *http.Request) int64 {
	if u, ok := identity.AuthUser(r); ok && u.EntityID != 0 {
		return u.EntityID
	}
	return 1
}

func storeErrorCode(err error) int {
	switch {
	case errors.Is(err, identity.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, identity.ErrVersionConflict):
		return http.StatusConflict
	case err != nil && strings.Contains(err.Error(), "duplicate"):
		return http.StatusConflict
	case err != nil && strings.Contains(err.Error(), "not found"):
		return http.StatusNotFound
	case err != nil && strings.Contains(err.Error(), "conflict"):
		return http.StatusConflict
	default:
		return http.StatusUnprocessableEntity
	}
}

func pathID(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func (h *Handler) publish(ctx context.Context, subject, entity string, id int64) {
	if h.deps.Bus == nil {
		return
	}
	_ = h.deps.Bus.Publish(ctx, platform.Event{Subject: subject, Entity: entity, ID: id})
}

type intentIn struct {
	OrgID     int64  `json:"org_id"`
	InvoiceID *int64 `json:"invoice_id"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
	Provider  string `json:"provider"`
}

// CreateIntent starts a collection: manual settles immediately, online
// providers return a client reference for the frontend to confirm.
func (h *Handler) CreateIntent(w http.ResponseWriter, r *http.Request) {
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
	clientRef, err := prov.CreateIntent(in.Amount, in.Currency)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	a := &PaymentAttempt{EntityID: entityOf(r), Ref: clientRef, OrgID: in.OrgID,
		InvoiceID: in.InvoiceID, Amount: in.Amount, Currency: in.Currency,
		Provider: in.Provider, Status: AttemptPending}
	if err := a.Validate(); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := h.deps.Store.CreateAttempt(r.Context(), a); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	if in.Provider == ProviderManual {
		settled, err := h.deps.Store.SetAttemptStatus(r.Context(), a.ID, AttemptSucceeded, a.RowVersion)
		if err != nil {
			writeErr(w, storeErrorCode(err), err.Error())
			return
		}
		*a = settled
	}
	h.publish(r.Context(), "forgeerp.payments.attempt.created.v1", "attempt", a.ID)
	writeJSON(w, http.StatusCreated, map[string]any{"attempt": a, "client_ref": clientRef})
}

// ListAttempts pages attempts within the caller's entity.
func (h *Handler) ListAttempts(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	list, err := h.deps.Store.ListAttempts(r.Context(), entityOf(r), limit, offset)
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
	a, err := h.deps.Store.SetAttemptStatus(r.Context(), id, AttemptStatus(in.Status), in.RowVersion)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a)
}

type stripeEvent struct {
	ID        string `json:"id"`         // provider event id (idempotency scope)
	AttemptID int64  `json:"attempt_id"` // our attempt
	Type      string `json:"type"`       // payment_intent.succeeded|payment_intent.payment_failed
}

// StripeWebhook applies a verified provider event idempotently: redelivery of
// a terminal attempt returns {duplicate:true} instead of re-transitioning.
func (h *Handler) StripeWebhook(w http.ResponseWriter, r *http.Request) {
	if h.deps.WebhookSecret == "" {
		writeErr(w, http.StatusServiceUnavailable, "payments: webhook not configured")
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
	a, err := h.deps.Store.AttemptByID(r.Context(), ev.AttemptID)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	if a.Status != AttemptPending {
		writeJSON(w, http.StatusOK, map[string]any{"duplicate": true, "attempt": a})
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
	upd, err := h.deps.Store.SetAttemptStatus(r.Context(), a.ID, to, a.RowVersion)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	h.publish(r.Context(), "forgeerp.payments.attempt."+strings.ToLower(ev.Type)+".v1", "attempt", upd.ID)
	writeJSON(w, http.StatusOK, map[string]any{"duplicate": false, "attempt": upd})
}
