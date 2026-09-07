package agenda

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
	Now   func() time.Time
}

// Middleware builds Require-style RBAC gates (identity.Handler.Require in production).
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the agenda surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("agenda", "event", "write")).Post("/agenda/events", h.CreateEvent)
	r.With(mw("agenda", "event", "read")).Get("/agenda/events", h.ListEvents)
	r.With(mw("agenda", "event", "validate")).Post("/agenda/events/{id}/status", h.SetEventStatus)
	r.With(mw("agenda", "reminder", "write")).Post("/agenda/reminders/dispatch", h.DispatchReminders)
}

// Handler implements the agenda HTTP surface.
type Handler struct{ deps Deps }

func (h *Handler) now() time.Time {
	if h.deps.Now != nil {
		return h.deps.Now()
	}
	return time.Now().UTC()
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

// CreateEvent schedules a calendar event.
func (h *Handler) CreateEvent(w http.ResponseWriter, r *http.Request) {
	var e Event
	if err := decode(r, &e); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	e.ID = 0
	e.EntityID = entityOf(r)
	e.Status = EventScheduled
	if err := h.deps.Store.CreateEvent(r.Context(), &e); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	h.publish(r.Context(), "forgeerp.agenda.event.created.v1", "event", e.ID)
	writeJSON(w, http.StatusCreated, e)
}

// ListEvents lists events intersecting [from, to).
func (h *Handler) ListEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, err1 := time.Parse(time.RFC3339, q.Get("from"))
	to, err2 := time.Parse(time.RFC3339, q.Get("to"))
	if err1 != nil || err2 != nil || !to.After(from) {
		writeErr(w, http.StatusBadRequest, "valid from/to (RFC3339) required")
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	list, err := h.deps.Store.ListEvents(r.Context(), entityOf(r), from, to, limit, 0)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// SetEventStatus completes or cancels an event.
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
	e, err := h.deps.Store.SetEventStatus(r.Context(), id, EventStatus(in.Status), in.RowVersion)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// DispatchReminders returns due reminders and stamps them sent (scheduler/cron
// hits this; each reminder fires exactly once via MarkReminded).
func (h *Handler) DispatchReminders(w http.ResponseWriter, r *http.Request) {
	now := h.now()
	due, err := h.deps.Store.DueReminders(r.Context(), entityOf(r), now, 100)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "dispatch failed")
		return
	}
	sent := 0
	for _, e := range due {
		if err := h.deps.Store.MarkReminded(r.Context(), e.ID); err != nil {
			continue
		}
		h.publish(r.Context(), "forgeerp.agenda.reminder.due.v1", "event", e.ID)
		sent++
	}
	writeJSON(w, http.StatusOK, map[string]any{"dispatched": sent, "events": due})
}
