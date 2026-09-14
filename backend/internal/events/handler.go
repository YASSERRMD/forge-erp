package events

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Deps wires handlers to persistence and the event bus.
type Deps struct {
	Store Store
	Bus   platform.Bus
	DB    platform.DBTX
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

type statusIn struct {
	Status     int16 `json:"status"`
	RowVersion int64 `json:"row_version"`
}

// CreateEvent opens a draft event.
func (h *Handler) CreateEvent(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var e OrgEvent
	if err := decode(r, &e); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	e.ID = 0
	e.EntityID = entityID
	e.Status = OrgEventDraft
	if err := h.deps.Store.CreateEvent(r.Context(), h.deps.DB, &e); err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.events.created.v1", "event", e.ID)
	writeJSON(w, http.StatusCreated, e)
}

// ListEvents lists events.
func (h *Handler) ListEvents(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	list, err := h.deps.Store.ListEvents(r.Context(), h.deps.DB, entityID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// SetEventStatus moves an event along its lifecycle.
func (h *Handler) SetEventStatus(w http.ResponseWriter, r *http.Request) {
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
	e, err := h.deps.Store.SetEventStatus(r.Context(), h.deps.DB, entityID, id, OrgEventStatus(in.Status), in.RowVersion)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// Register books a seat after capacity checks.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
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
	reg.EntityID = entityID
	reg.EventID = eid
	reg.Status = RegRegistered
	if err := h.deps.Store.Register(r.Context(), h.deps.DB, &reg); err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.events.registered.v1", "registration", reg.ID)
	writeJSON(w, http.StatusCreated, reg)
}

// ListRegistrations lists an event's registrations.
func (h *Handler) ListRegistrations(w http.ResponseWriter, r *http.Request) {
	eid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	list, err := h.deps.Store.RegistrationsOf(r.Context(), h.deps.DB, eid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// SetRegistrationStatus moves a registration along its flow.
func (h *Handler) SetRegistrationStatus(w http.ResponseWriter, r *http.Request) {
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
	reg, err := h.deps.Store.SetRegistrationStatus(r.Context(), h.deps.DB, entityID, id, RegistrationStatus(in.Status))
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reg)
}

// CreatePosition opens a draft job posting.
func (h *Handler) CreatePosition(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var p Position
	if err := decode(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	p.ID = 0
	p.EntityID = entityID
	p.Status = PositionDraft
	if err := h.deps.Store.CreatePosition(r.Context(), h.deps.DB, &p); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

// ListPositions lists job postings.
func (h *Handler) ListPositions(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	list, err := h.deps.Store.ListPositions(r.Context(), h.deps.DB, entityID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// SetPositionStatus opens or closes a posting.
func (h *Handler) SetPositionStatus(w http.ResponseWriter, r *http.Request) {
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
	p, err := h.deps.Store.SetPositionStatus(r.Context(), h.deps.DB, entityID, id, PositionStatus(in.Status), in.RowVersion)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// Apply submits a candidate to an open position.
func (h *Handler) Apply(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
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
	a.EntityID = entityID
	a.PositionID = pid
	a.Status = AppReceived
	if err := h.deps.Store.Apply(r.Context(), h.deps.DB, &a); err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.events.application.received.v1", "application", a.ID)
	writeJSON(w, http.StatusCreated, a)
}

// ListApplications lists a position's candidates.
func (h *Handler) ListApplications(w http.ResponseWriter, r *http.Request) {
	pid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	list, err := h.deps.Store.ApplicationsOf(r.Context(), h.deps.DB, pid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// SetApplicationStatus moves a candidate along the pipeline.
func (h *Handler) SetApplicationStatus(w http.ResponseWriter, r *http.Request) {
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
	a, err := h.deps.Store.SetApplicationStatus(r.Context(), h.deps.DB, entityID, id, ApplicationStatus(in.Status), in.RowVersion)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}
