package booking

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

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

// Routes mounts the booking surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("booking", "resource", "write")).Post("/booking/resources", h.CreateResource)
	r.With(mw("booking", "resource", "read")).Get("/booking/resources", h.ListResources)
	r.With(mw("booking", "booking", "write")).Post("/bookings", h.CreateBooking)
	r.With(mw("booking", "booking", "read")).Get("/bookings", h.ListBookings)
	r.With(mw("booking", "booking", "validate")).Post("/bookings/{id}/status", h.SetBookingStatus)
}

// Handler implements the booking HTTP surface.
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

type statusIn struct {
	Status     int16 `json:"status"`
	RowVersion int64 `json:"row_version"`
}

// CreateResource registers a bookable resource.
func (h *Handler) CreateResource(w http.ResponseWriter, r *http.Request) {
	var res Resource
	if err := decode(r, &res); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	res.ID = 0
	res.EntityID = entityOf(r)
	res.Status = ResourceActive
	if err := h.deps.Store.CreateResource(r.Context(), &res); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

// ListResources lists resources within the caller's entity.
func (h *Handler) ListResources(w http.ResponseWriter, r *http.Request) {
	list, err := h.deps.Store.ListResources(r.Context(), entityOf(r))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// CreateBooking reserves a window after capacity checks (422 on overlap).
func (h *Handler) CreateBooking(w http.ResponseWriter, r *http.Request) {
	var b Booking
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	b.ID = 0
	b.EntityID = entityOf(r)
	b.Status = BookingBooked
	if err := h.deps.Store.CreateBooking(r.Context(), &b); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	h.publish(r.Context(), "forgeerp.booking.created.v1", "booking", b.ID)
	writeJSON(w, http.StatusCreated, b)
}

// ListBookings lists a resource's bookings in [from, to).
func (h *Handler) ListBookings(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	resID, err := strconv.ParseInt(q.Get("resource_id"), 10, 64)
	if err != nil || resID <= 0 {
		writeErr(w, http.StatusBadRequest, "resource_id required")
		return
	}
	from, err1 := time.Parse(time.RFC3339, q.Get("from"))
	to, err2 := time.Parse(time.RFC3339, q.Get("to"))
	if err1 != nil || err2 != nil || !to.After(from) {
		writeErr(w, http.StatusBadRequest, "valid from/to (RFC3339) required")
		return
	}
	list, err := h.deps.Store.BookingsOf(r.Context(), resID, from, to)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// SetBookingStatus moves a booking along its lifecycle.
func (h *Handler) SetBookingStatus(w http.ResponseWriter, r *http.Request) {
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
	b, err := h.deps.Store.SetBookingStatus(r.Context(), id, BookingStatus(in.Status), in.RowVersion)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, b)
}
