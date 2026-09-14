package services

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

// Deps wires handlers to persistence and the event bus (bus may be nil in tests).
type Deps struct {
	Store Store
	Bus   platform.Bus
	DB    platform.DBTX
}

// Middleware builds Require-style RBAC gates (identity.Handler.Require in production).
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the services surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("services", "project", "write")).Post("/services/projects", h.CreateProject)
	r.With(mw("services", "project", "read")).Get("/services/projects", h.ListProjects)
	r.With(mw("services", "project", "read")).Get("/services/projects/{id}", h.GetProject)
	r.With(mw("services", "project", "validate")).Post("/services/projects/{id}/status", h.SetProjectStatus)
	r.With(mw("services", "task", "write")).Post("/services/projects/{id}/tasks", h.CreateTask)
	r.With(mw("services", "task", "read")).Get("/services/projects/{id}/tasks", h.ListTasks)
	r.With(mw("services", "task", "validate")).Post("/services/tasks/{id}/status", h.SetTaskStatus)
	r.With(mw("services", "time", "write")).Post("/services/tasks/{id}/time", h.AddTime)
	r.With(mw("services", "time", "read")).Get("/services/tasks/{id}/hours", h.TaskHours)
	r.With(mw("services", "time", "read")).Get("/services/projects/{id}/hours", h.ProjectHours)
	r.With(mw("services", "contract", "write")).Post("/services/contracts", h.CreateContract)
	r.With(mw("services", "contract", "read")).Get("/services/contracts", h.ListContracts)
	r.With(mw("services", "contract", "validate")).Post("/services/contracts/{id}/status", h.SetContractStatus)
	r.With(mw("services", "contract", "read")).Get("/services/organizations/{orgID}/contracts", h.ContractsOfOrg)
	r.With(mw("services", "intervention", "write")).Post("/services/interventions", h.CreateIntervention)
	r.With(mw("services", "intervention", "read")).Get("/services/interventions", h.ListInterventions)
	r.With(mw("services", "intervention", "validate")).Post("/services/interventions/{id}/status", h.SetInterventionStatus)
	r.With(mw("services", "ticket", "write")).Post("/services/tickets", h.CreateTicket)
	r.With(mw("services", "ticket", "read")).Get("/services/tickets", h.ListTickets)
	r.With(mw("services", "ticket", "read")).Get("/services/tickets/{id}", h.GetTicket)
	r.With(mw("services", "ticket", "validate")).Post("/services/tickets/{id}/status", h.SetTicketStatus)
	r.With(mw("services", "ticket", "write")).Post("/services/tickets/{id}/messages", h.AddMessage)
	r.With(mw("services", "ticket", "read")).Get("/services/tickets/{id}/messages", h.ListMessages)
}

// Handler implements the services HTTP surface.
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

// CreateProject opens a draft project (422 on validation/duplicate ref).
func (h *Handler) CreateProject(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var p Project
	if err := decode(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	p.ID = 0
	p.EntityID = entityID
	p.Status = ProjectDraft
	if err := h.deps.Store.CreateProject(r.Context(), h.deps.DB, &p); err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.services.project.created.v1", "project", p.ID)
	writeJSON(w, http.StatusCreated, p)
}

// ListProjects pages projects within the caller's entity.
func (h *Handler) ListProjects(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	limit, offset := page(r)
	list, err := h.deps.Store.ListProjects(r.Context(), h.deps.DB, entityID, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// GetProject fetches one project.
func (h *Handler) GetProject(w http.ResponseWriter, r *http.Request) {
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
	p, err := h.deps.Store.ProjectByID(r.Context(), h.deps.DB, entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// SetProjectStatus moves a project along its state machine.
func (h *Handler) SetProjectStatus(w http.ResponseWriter, r *http.Request) {
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
	p, err := h.deps.Store.SetProjectStatus(r.Context(), h.deps.DB, entityID, id, ProjectStatus(in.Status), in.RowVersion)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.services.project.status.v1", "project", p.ID)
	writeJSON(w, http.StatusOK, p)
}

// CreateTask adds a task to an open project.
func (h *Handler) CreateTask(w http.ResponseWriter, r *http.Request) {
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
	var t Task
	if err := decode(r, &t); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	t.ID = 0
	t.EntityID = entityID
	t.ProjectID = pid
	t.Status = TaskTodo
	if err := h.deps.Store.CreateTask(r.Context(), h.deps.DB, &t); err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.services.task.created.v1", "task", t.ID)
	writeJSON(w, http.StatusCreated, t)
}

// ListTasks lists a project's tasks.
func (h *Handler) ListTasks(w http.ResponseWriter, r *http.Request) {
	pid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	list, err := h.deps.Store.TasksOf(r.Context(), h.deps.DB, pid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// SetTaskStatus moves a task along its state machine.
func (h *Handler) SetTaskStatus(w http.ResponseWriter, r *http.Request) {
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
	t, err := h.deps.Store.SetTaskStatus(r.Context(), h.deps.DB, entityID, id, TaskStatus(in.Status), in.RowVersion)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// AddTime books hours against an open task.
func (h *Handler) AddTime(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	tid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var e TimeEntry
	if err := decode(r, &e); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	e.ID = 0
	e.EntityID = entityID
	e.TaskID = tid
	if err := h.deps.Store.AddTime(r.Context(), h.deps.DB, &e); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, e)
}

// TaskHours returns booked hundredths for a task.
func (h *Handler) TaskHours(w http.ResponseWriter, r *http.Request) {
	tid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	sum, err := h.deps.Store.TaskHours(r.Context(), h.deps.DB, tid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "sum failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"task_id": tid, "hours": sum})
}

// ProjectHours returns booked hundredths for a project.
func (h *Handler) ProjectHours(w http.ResponseWriter, r *http.Request) {
	pid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	sum, err := h.deps.Store.ProjectHours(r.Context(), h.deps.DB, pid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "sum failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"project_id": pid, "hours": sum})
}

// CreateContract opens a draft service contract.
func (h *Handler) CreateContract(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var c ServiceContract
	if err := decode(r, &c); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	c.ID = 0
	c.EntityID = entityID
	c.Status = ContractDraft
	if err := h.deps.Store.CreateContract(r.Context(), h.deps.DB, &c); err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.services.contract.created.v1", "contract", c.ID)
	writeJSON(w, http.StatusCreated, c)
}

// SetContractStatus moves a contract along its state machine.
func (h *Handler) SetContractStatus(w http.ResponseWriter, r *http.Request) {
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
	c, err := h.deps.Store.SetContractStatus(r.Context(), h.deps.DB, entityID, id, ContractStatus(in.Status), in.RowVersion)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// ListContracts pages contracts within the caller's entity.
func (h *Handler) ListContracts(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	limit, offset := page(r)
	list, err := h.deps.Store.ListContracts(r.Context(), h.deps.DB, entityID, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// ContractsOfOrg lists an organization's contracts.
func (h *Handler) ContractsOfOrg(w http.ResponseWriter, r *http.Request) {
	orgID, ok := pathID(r, "orgID")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad org id")
		return
	}
	list, err := h.deps.Store.ContractsOfOrg(r.Context(), h.deps.DB, orgID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// CreateIntervention schedules a field intervention.
func (h *Handler) CreateIntervention(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var in Intervention
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	in.ID = 0
	in.EntityID = entityID
	in.Status = InterventionScheduled
	if err := h.deps.Store.CreateIntervention(r.Context(), h.deps.DB, &in); err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.services.intervention.created.v1", "intervention", in.ID)
	writeJSON(w, http.StatusCreated, in)
}

// ListInterventions pages interventions within the caller's entity.
func (h *Handler) ListInterventions(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	limit, offset := page(r)
	list, err := h.deps.Store.ListInterventions(r.Context(), h.deps.DB, entityID, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// SetInterventionStatus moves an intervention along its state machine.
func (h *Handler) SetInterventionStatus(w http.ResponseWriter, r *http.Request) {
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
	upd, err := h.deps.Store.SetInterventionStatus(r.Context(), h.deps.DB, entityID, id, InterventionStatus(in.Status), in.RowVersion)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, upd)
}

// CreateTicket opens a ticket.
func (h *Handler) CreateTicket(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var t Ticket
	if err := decode(r, &t); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	t.ID = 0
	t.EntityID = entityID
	t.Status = TicketOpen
	if err := h.deps.Store.CreateTicket(r.Context(), h.deps.DB, &t); err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.services.ticket.created.v1", "ticket", t.ID)
	writeJSON(w, http.StatusCreated, t)
}

// ListTickets pages tickets within the caller's entity.
func (h *Handler) ListTickets(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	limit, offset := page(r)
	list, err := h.deps.Store.ListTickets(r.Context(), h.deps.DB, entityID, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// GetTicket fetches one ticket.
func (h *Handler) GetTicket(w http.ResponseWriter, r *http.Request) {
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
	t, err := h.deps.Store.TicketByID(r.Context(), h.deps.DB, entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// SetTicketStatus moves a ticket along its state machine.
func (h *Handler) SetTicketStatus(w http.ResponseWriter, r *http.Request) {
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
	t, err := h.deps.Store.SetTicketStatus(r.Context(), h.deps.DB, entityID, id, TicketStatus(in.Status), in.RowVersion)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// AddMessage appends a helpdesk message to an open ticket.
func (h *Handler) AddMessage(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	tid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var m TicketMessage
	if err := decode(r, &m); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	m.ID = 0
	m.EntityID = entityID
	m.TicketID = tid
	if err := h.deps.Store.AddMessage(r.Context(), h.deps.DB, &m); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

// ListMessages lists a ticket's messages.
func (h *Handler) ListMessages(w http.ResponseWriter, r *http.Request) {
	tid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	list, err := h.deps.Store.MessagesOf(r.Context(), h.deps.DB, tid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}
