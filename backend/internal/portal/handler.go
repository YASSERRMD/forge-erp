package portal

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Deps wires handlers to token persistence, the sales/services seams, and events.
type Deps struct {
	Store    Store
	Sales    Sales
	Services Services
	Bus      platform.Bus
	DB       platform.DBTX
	Pool     *pgxpool.Pool // reserved for transaction-scoped portal flows (nil in tests)
}

// Middleware builds Require-style RBAC gates (identity.Handler.Require in
// production). It guards the STAFF token-mint endpoint only; customer routes
// use the portal token middleware below.
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the portal surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d, svc: &Service{Store: d.Store, Sales: d.Sales,
		Services: d.Services, Bus: d.Bus, DB: d.DB}}
	// Staff side (RBAC): mint/revoke customer credentials.
	r.With(mw("portal", "token", "write")).Post("/portal/tokens", h.MintToken)
	r.With(mw("portal", "token", "write")).Post("/portal/tokens/{id}/revoke", h.RevokeToken)
	// Customer side (bearer token): self-service.
	r.With(h.RequireToken).Get("/portal/invoices", h.ListInvoices)
	r.With(h.RequireToken).Get("/portal/invoices/{id}", h.GetInvoice)
	r.With(h.RequireToken).Get("/portal/quotes", h.ListQuotes)
	r.With(h.RequireToken).Post("/portal/quotes/{id}/accept", h.AcceptQuote)
	r.With(h.RequireToken).Get("/portal/tickets", h.ListTickets)
	r.With(h.RequireToken).Get("/portal/tickets/{id}", h.GetTicket)
	r.With(h.RequireToken).Post("/portal/tickets", h.OpenTicket)
}

// Handler implements the portal HTTP surface.
type Handler struct {
	deps Deps
	svc  *Service
}

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

func pageParams(r *http.Request) (limit, offset int) {
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func pathID(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return ""
	}
	return strings.TrimSpace(h[7:])
}

// RequireToken enforces portal customer auth: the bearer token is hashed and
// resolved to a live row; missing/unknown/expired/revoked tokens get 401
// (the entity comes FROM the token — customers carry no JWT). The resolved
// customer lands in the request context (IdentityOf) alongside the platform
// entity (EntityOf) so service calls stay tenant-scoped.
func (h *Handler) RequireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := bearer(r)
		if raw == "" {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		salt, secret := SplitBearer(raw)
		if salt == "" || secret == "" {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		t, err := h.deps.Store.TokenByHash(r.Context(), h.deps.DB, HashToken(salt, secret))
		if err != nil || t.Salt != salt || !VerifyToken(t, secret) {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		id := Identity{EntityID: t.EntityID, OrgID: t.OrgID, ContactID: t.ContactID, TokenID: t.ID}
		ctx := ContextWithIdentity(r.Context(), id)
		ctx = platform.ContextWithEntity(ctx, t.EntityID)
		platform.ReqCtxLogger(ctx, nil).Debug("portal: authenticated request", "org", id.OrgID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// SplitBearer splits a "<salt>.<secret>" credential. Salts are non-secret
// (crypt/MCF practice: they only stop rainbow tables), so embedding them in
// the credential buys a single indexed hash lookup at auth time plus a
// constant-time verify afterwards.
func SplitBearer(raw string) (salt, secret string) {
	i := strings.Index(raw, ".")
	if i <= 0 || i == len(raw)-1 {
		return "", ""
	}
	return raw[:i], raw[i+1:]
}

type mintIn struct {
	OrgID     int64  `json:"org_id"`
	ContactID *int64 `json:"contact_id"`
	TTLDays   int    `json:"ttl_days"`
}

// MintToken mints a customer credential (staff, RBAC-gated). The raw secret
// is returned once in `token`; only its hash is stored.
func (h *Handler) MintToken(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var in mintIn
	if err := decode(r, &in); err != nil || in.OrgID <= 0 {
		writeErr(w, http.StatusBadRequest, "org_id required")
		return
	}
	ttl := time.Duration(in.TTLDays) * 24 * time.Hour
	if in.TTLDays <= 0 {
		ttl = TokenTTL
	}
	row, credential, err := h.svc.MintToken(r.Context(), entityID, in.OrgID, in.ContactID, ttl)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"token": credential, "id": row.ID,
		"org_id": row.OrgID, "expires_at": row.ExpiresAt,
	})
}

// RevokeToken kills a customer credential (staff, RBAC-gated).
func (h *Handler) RevokeToken(w http.ResponseWriter, r *http.Request) {
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
	if err := h.svc.RevokeToken(r.Context(), entityID, id); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "revoked": true})
}

func portalID(w http.ResponseWriter, r *http.Request) (Identity, bool) {
	id, err := IdentityOf(r)
	if err != nil {
		platform.WriteError(w, err)
		return Identity{}, false
	}
	return id, true
}

// ListInvoices returns the customer's own invoices.
func (h *Handler) ListInvoices(w http.ResponseWriter, r *http.Request) {
	id, ok := portalID(w, r)
	if !ok {
		return
	}
	limit, offset := pageParams(r)
	list, err := h.svc.ListInvoices(r.Context(), id, limit, offset)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// GetInvoice returns one own invoice.
func (h *Handler) GetInvoice(w http.ResponseWriter, r *http.Request) {
	id, ok := portalID(w, r)
	if !ok {
		return
	}
	docID, valid := pathID(r, "id")
	if !valid {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	d, err := h.svc.GetInvoice(r.Context(), id, docID)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// ListQuotes returns the customer's own proposals.
func (h *Handler) ListQuotes(w http.ResponseWriter, r *http.Request) {
	id, ok := portalID(w, r)
	if !ok {
		return
	}
	limit, offset := pageParams(r)
	list, err := h.svc.ListQuotes(r.Context(), id, limit, offset)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// AcceptQuote signs the customer's own validated proposal.
func (h *Handler) AcceptQuote(w http.ResponseWriter, r *http.Request) {
	id, ok := portalID(w, r)
	if !ok {
		return
	}
	docID, valid := pathID(r, "id")
	if !valid {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	d, err := h.svc.AcceptQuote(r.Context(), id, docID)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// ListTickets returns the customer's own tickets.
func (h *Handler) ListTickets(w http.ResponseWriter, r *http.Request) {
	id, ok := portalID(w, r)
	if !ok {
		return
	}
	limit, offset := pageParams(r)
	list, err := h.svc.ListTickets(r.Context(), id, limit, offset)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// GetTicket returns one own ticket.
func (h *Handler) GetTicket(w http.ResponseWriter, r *http.Request) {
	id, ok := portalID(w, r)
	if !ok {
		return
	}
	ticketID, valid := pathID(r, "id")
	if !valid {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	t, err := h.svc.GetTicket(r.Context(), id, ticketID)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

type openTicketIn struct {
	Subject  string `json:"subject"`
	Priority int16  `json:"priority"`
}

// OpenTicket files a support ticket as the customer's org.
func (h *Handler) OpenTicket(w http.ResponseWriter, r *http.Request) {
	id, ok := portalID(w, r)
	if !ok {
		return
	}
	var in openTicketIn
	if err := decode(r, &in); err != nil || strings.TrimSpace(in.Subject) == "" {
		writeErr(w, http.StatusBadRequest, "subject required")
		return
	}
	t, err := h.svc.OpenTicket(r.Context(), id, strings.TrimSpace(in.Subject), in.Priority)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}
