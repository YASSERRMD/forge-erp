package members

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Deps wires handlers to persistence and the event bus.
type Deps struct {
	Store Store
	Bus   platform.Bus
}

// Middleware builds Require-style RBAC gates (identity.Handler.Require in production).
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the members surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("members", "type", "write")).Post("/member-types", h.CreateType)
	r.With(mw("members", "type", "read")).Get("/member-types", h.ListTypes)
	r.With(mw("members", "member", "write")).Post("/members", h.CreateMember)
	r.With(mw("members", "member", "read")).Get("/members", h.ListMembers)
	r.With(mw("members", "member", "validate")).Post("/members/{id}/status", h.SetMemberStatus)
	r.With(mw("members", "subscription", "write")).Post("/members/{id}/subscriptions", h.CreateSubscription)
	r.With(mw("members", "subscription", "read")).Get("/members/{id}/subscriptions", h.ListSubscriptions)
	r.With(mw("members", "subscription", "validate")).Post("/subscriptions/{id}/status", h.SetSubscriptionStatus)
	r.With(mw("members", "donation", "write")).Post("/donations", h.CreateDonation)
	r.With(mw("members", "donation", "read")).Get("/donations", h.ListDonations)
	r.With(mw("members", "donation", "validate")).Post("/donations/{id}/status", h.SetDonationStatus)
}

// Handler implements the members HTTP surface.
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

func page(r *http.Request) (int, int) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func (h *Handler) publish(ctx context.Context, subject, entity string, id int64) {
	if h.deps.Bus == nil {
		return
	}
	_ = h.deps.Bus.Publish(ctx, platform.Event{Subject: subject, Entity: entity, ID: id})
}

type statusIn struct {
	Status     int16 `json:"status"`
	RowVersion int64 `json:"row_version"`
}

// CreateType registers a membership class.
func (h *Handler) CreateType(w http.ResponseWriter, r *http.Request) {
	var t MemberType
	if err := decode(r, &t); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	t.ID = 0
	t.EntityID = entityOf(r)
	if err := h.deps.Store.CreateType(r.Context(), &t); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

// ListTypes lists membership classes.
func (h *Handler) ListTypes(w http.ResponseWriter, r *http.Request) {
	list, err := h.deps.Store.ListTypes(r.Context(), entityOf(r))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// CreateMember registers a draft member.
func (h *Handler) CreateMember(w http.ResponseWriter, r *http.Request) {
	var m Member
	if err := decode(r, &m); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	m.ID = 0
	m.EntityID = entityOf(r)
	m.Status = MemberDraft
	if err := h.deps.Store.CreateMember(r.Context(), &m); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	h.publish(r.Context(), "forgeerp.members.created.v1", "member", m.ID)
	writeJSON(w, http.StatusCreated, m)
}

// ListMembers pages members.
func (h *Handler) ListMembers(w http.ResponseWriter, r *http.Request) {
	limit, offset := page(r)
	list, err := h.deps.Store.ListMembers(r.Context(), entityOf(r), limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// SetMemberStatus moves a member along its lifecycle.
func (h *Handler) SetMemberStatus(w http.ResponseWriter, r *http.Request) {
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
	m, err := h.deps.Store.SetMemberStatus(r.Context(), id, MemberStatus(in.Status), in.RowVersion)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// CreateSubscription opens a draft yearly subscription.
func (h *Handler) CreateSubscription(w http.ResponseWriter, r *http.Request) {
	mid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var s Subscription
	if err := decode(r, &s); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	s.ID = 0
	s.EntityID = entityOf(r)
	s.MemberID = mid
	s.Status = SubDraft
	if err := h.deps.Store.CreateSubscription(r.Context(), &s); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, s)
}

// ListSubscriptions lists a member's subscriptions.
func (h *Handler) ListSubscriptions(w http.ResponseWriter, r *http.Request) {
	mid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	list, err := h.deps.Store.SubscriptionsOf(r.Context(), mid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// SetSubscriptionStatus moves a subscription along its flow.
func (h *Handler) SetSubscriptionStatus(w http.ResponseWriter, r *http.Request) {
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
	s, err := h.deps.Store.SetSubscriptionStatus(r.Context(), id, SubscriptionStatus(in.Status), in.RowVersion)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// CreateDonation records a promised donation.
func (h *Handler) CreateDonation(w http.ResponseWriter, r *http.Request) {
	var d Donation
	if err := decode(r, &d); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	d.ID = 0
	d.EntityID = entityOf(r)
	d.Status = DonationPromised
	if err := h.deps.Store.CreateDonation(r.Context(), &d); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	h.publish(r.Context(), "forgeerp.donation.created.v1", "donation", d.ID)
	writeJSON(w, http.StatusCreated, d)
}

// ListDonations pages donations.
func (h *Handler) ListDonations(w http.ResponseWriter, r *http.Request) {
	limit, offset := page(r)
	list, err := h.deps.Store.ListDonations(r.Context(), entityOf(r), limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// SetDonationStatus settles or cancels a donation.
func (h *Handler) SetDonationStatus(w http.ResponseWriter, r *http.Request) {
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
	d, err := h.deps.Store.SetDonationStatus(r.Context(), id, DonationStatus(in.Status), in.RowVersion)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, d)
}
