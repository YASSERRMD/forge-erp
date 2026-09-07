package survey

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

// Routes mounts the survey surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("survey", "survey", "write")).Post("/surveys", h.CreateSurvey)
	r.With(mw("survey", "survey", "read")).Get("/surveys", h.ListSurveys)
	r.With(mw("survey", "survey", "validate")).Post("/surveys/{id}/status", h.SetSurveyStatus)
	r.With(mw("survey", "question", "write")).Post("/surveys/{id}/questions", h.AddQuestion)
	r.With(mw("survey", "question", "read")).Get("/surveys/{id}/questions", h.ListQuestions)
	r.With(mw("survey", "option", "write")).Post("/questions/{id}/options", h.AddOption)
	r.With(mw("survey", "option", "read")).Get("/questions/{id}/options", h.ListOptions)
	r.With(mw("survey", "vote", "write")).Post("/questions/{id}/votes", h.CastVote)
	r.With(mw("survey", "vote", "read")).Get("/questions/{id}/results", h.Results)
}

// Handler implements the survey HTTP surface.
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

// CreateSurvey opens a draft survey.
func (h *Handler) CreateSurvey(w http.ResponseWriter, r *http.Request) {
	var s Survey
	if err := decode(r, &s); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	s.ID = 0
	s.EntityID = entityOf(r)
	s.Status = SurveyDraft
	if err := h.deps.Store.CreateSurvey(r.Context(), &s); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	h.publish(r.Context(), "forgeerp.survey.created.v1", "survey", s.ID)
	writeJSON(w, http.StatusCreated, s)
}

// ListSurveys lists surveys within the caller's entity.
func (h *Handler) ListSurveys(w http.ResponseWriter, r *http.Request) {
	list, err := h.deps.Store.ListSurveys(r.Context(), entityOf(r))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// SetSurveyStatus opens or closes a survey.
func (h *Handler) SetSurveyStatus(w http.ResponseWriter, r *http.Request) {
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
	s, err := h.deps.Store.SetSurveyStatus(r.Context(), id, SurveyStatus(in.Status), in.RowVersion)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// AddQuestion appends a question to a draft survey.
func (h *Handler) AddQuestion(w http.ResponseWriter, r *http.Request) {
	sid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var q Question
	if err := decode(r, &q); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	q.ID = 0
	q.EntityID = entityOf(r)
	q.SurveyID = sid
	if err := h.deps.Store.AddQuestion(r.Context(), &q); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, q)
}

// ListQuestions lists a survey's questions.
func (h *Handler) ListQuestions(w http.ResponseWriter, r *http.Request) {
	sid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	list, err := h.deps.Store.QuestionsOf(r.Context(), sid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// AddOption appends an answer choice.
func (h *Handler) AddOption(w http.ResponseWriter, r *http.Request) {
	qid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var o Option
	if err := decode(r, &o); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	o.ID = 0
	o.EntityID = entityOf(r)
	o.QuestionID = qid
	if err := h.deps.Store.AddOption(r.Context(), &o); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, o)
}

// ListOptions lists a question's choices.
func (h *Handler) ListOptions(w http.ResponseWriter, r *http.Request) {
	qid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	list, err := h.deps.Store.OptionsOf(r.Context(), qid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// CastVote records or replaces one ballot.
func (h *Handler) CastVote(w http.ResponseWriter, r *http.Request) {
	qid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var v Vote
	if err := decode(r, &v); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	v.ID = 0
	v.EntityID = entityOf(r)
	v.QuestionID = qid
	if err := h.deps.Store.CastVote(r.Context(), &v); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

// Results serves the vote tally for a question.
func (h *Handler) Results(w http.ResponseWriter, r *http.Request) {
	qid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	tally, err := h.deps.Store.Results(r.Context(), qid)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tally)
}
