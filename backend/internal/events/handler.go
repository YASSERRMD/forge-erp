package events

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

// Routes mounts the events surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("events", "event", "write")).Post("/events", h.CreateEvent)
	r.With(mw("events", "event", "read")).Get("/events", h.ListEvents)
	r.With(mw("events", "event", "validate")).Post("/events/{id}/status", h.SetEventStatus)
	r.With(mw("events", "registration", "write")).Post("/events/{id}/registrations", h.Register)
	r.With(mw("events", "registration", "read")).Get("/events/{id}/registrations", h.ListRegistrations)
	r.With(mw("events", "registration", "validate")).Post("/registrations/{id}/status", h.SetRegistrationStatus)
	r.With(mw("events", "position", "write")).Post("/positions", h.CreatePosition)
	r.With(mw("events", "position", "read")).Get("/positions", h.ListPositions)
	r.With(mw("events", "position", "validate")).Post("/positions/{id}/status", h.SetPositionStatus)
	r.With(mw("events", "application", "write")).Post("/positions/{id}/applications", h.Apply)
	r.With(mw("events", "application", "read")).Get("/positions/{id}/applications", h.ListApplications)
	r.With(mw("events", "application", "validate")).Post("/applications/{id}/status", h.SetApplicationStatus)
}

// Handler implements the events HTTP surface.
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

// CreateEvent opens a draft event.
func (h *Handler) CreateEvent(w http.ResponseWriter, r *http.Request) {
	var e OrgEvent
	if err := decode(r, &e); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	e.ID = 0
	e.EntityID = entityOf(r)
	e.Status = OrgEventDraft
	if err := h.deps.Store.CreateEvent(r.Context(), &e); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	h.publish(r.Context(), "forgeerp.events.created.v1", "event", e.ID)
	writeJSON(w, http.StatusCreated, e)
}

// ListEvents lists events.
func (h *Handler) ListEvents(w http.ResponseWriter, r *http.Request) {
	list, err := h.deps.Store.ListEvents(r.Context(), entityOf(r))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// SetEventStatus moves an event along its lifecycle.
func (h *Handler) SetEventStatus(w http.ResponseWriter, r *http.Request) {
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
	e, err := h.deps.Store.SetEventStatus(r.Context(), id, OrgEventStatus(in.Status), in.RowVersion)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// Register books a seat after capacity checks.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	eid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var reg Registration
	if err := decode(r, &reg); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	reg.ID = 0
	reg.EntityID = entityOf(r)
	reg.EventID = eid
	reg.Status = RegRegistered
	if err := h.deps.Store.Register(r.Context(), &reg); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	h.publish(r.Context(), "forgeerp.events.registered.v1", "registration", reg.ID)
	writeJSON(w, http.StatusCreated, reg)
}

// ListRegistrations lists an event's registrations.
func (h *Handler) ListRegistrations(w http.ResponseWriter, r *http.Request) {
	eid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	list, err := h.deps.Store.RegistrationsOf(r.Context(), eid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// SetRegistrationStatus moves a registration along its flow.
func (h *Handler) SetRegistrationStatus(w http.ResponseWriter, r *http.Request) {
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
	reg, err := h.deps.Store.SetRegistrationStatus(r.Context(), id, RegistrationStatus(in.Status))
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, reg)
}

// CreatePosition opens a draft job posting.
func (h *Handler) CreatePosition(w http.ResponseWriter, r *http.Request) {
	var p Position
	if err := decode(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	p.ID = 0
	p.EntityID = entityOf(r)
	p.Status = PositionDraft
	if err := h.deps.Store.CreatePosition(r.Context(), &p); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

// ListPositions lists job postings.
func (h *Handler) ListPositions(w http.ResponseWriter, r *http.Request) {
	list, err := h.deps.Store.ListPositions(r.Context(), entityOf(r))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// SetPositionStatus opens or closes a posting.
func (h *Handler) SetPositionStatus(w http.ResponseWriter, r *http.Request) {
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
	p, err := h.deps.Store.SetPositionStatus(r.Context(), id, PositionStatus(in.Status), in.RowVersion)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// Apply submits a candidate to an open position.
func (h *Handler) Apply(w http.ResponseWriter, r *http.Request) {
	pid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var a Application
	if err := decode(r, &a); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	a.ID = 0
	a.EntityID = entityOf(r)
	a.PositionID = pid
	a.Status = AppReceived
	if err := h.deps.Store.Apply(r.Context(), &a); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	h.publish(r.Context(), "forgeerp.events.application.received.v1", "application", a.ID)
	writeJSON(w, http.StatusCreated, a)
}

// ListApplications lists a position's candidates.
func (h *Handler) ListApplications(w http.ResponseWriter, r *http.Request) {
	pid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	list, err := h.deps.Store.ApplicationsOf(r.Context(), pid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// SetApplicationStatus moves a candidate along the pipeline.
func (h *Handler) SetApplicationStatus(w http.ResponseWriter, r *http.Request) {
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
	a, err := h.deps.Store.SetApplicationStatus(r.Context(), id, ApplicationStatus(in.Status), in.RowVersion)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a)
}
