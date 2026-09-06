// Package services implements projects with tasks and time entries (Dolibarr
// llx_projet/llx_projet_task(+_time)), service contracts (llx_contrat),
// field interventions (llx_fichinter) and helpdesk tickets (llx_ticket).
package services

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Project status (Dolibarr llx_projet.status: 0 draft, 1 validated, 2 closed).
type ProjectStatus int16

const (
	ProjectDraft    ProjectStatus = 0
	ProjectActive   ProjectStatus = 1
	ProjectOnHold   ProjectStatus = 2
	ProjectClosed   ProjectStatus = 3
	ProjectCanceled ProjectStatus = -1
)

// Task status.
type TaskStatus int16

const (
	TaskTodo     TaskStatus = 0
	TaskDoing    TaskStatus = 1
	TaskDone     TaskStatus = 2
	TaskCanceled TaskStatus = -1
)

// Contract status (Dolibarr llx_contrat.statut: 0 draft, 1 validated/active).
type ContractStatus int16

const (
	ContractDraft     ContractStatus = 0
	ContractActive    ContractStatus = 1
	ContractSuspended ContractStatus = 2
	ContractClosed    ContractStatus = 3
	ContractCanceled  ContractStatus = -1
)

// Intervention status (Dolibarr llx_fichinter.fk_statut).
type InterventionStatus int16

const (
	InterventionScheduled  InterventionStatus = 0
	InterventionInProgress InterventionStatus = 1
	InterventionDone       InterventionStatus = 2
	InterventionCanceled   InterventionStatus = -1
)

// Ticket status (Dolibarr llx_ticket.fk_statut) and priorities.
type TicketStatus int16

const (
	TicketOpen     TicketStatus = 0
	TicketPending  TicketStatus = 1
	TicketResolved TicketStatus = 2
	TicketClosed   TicketStatus = 3
)

// Project is a work container scoped to an entity, optionally linked to an org.
type Project struct {
	ID          int64         `json:"id"`
	EntityID    int64         `json:"entity_id"`
	Ref         string        `json:"ref"` // unique per entity (Dolibarr llx_projet.ref)
	Label       string        `json:"label"`
	Description string        `json:"description"`
	OrgID       *int64        `json:"org_id"` // fk_soc equivalent, optional hub link
	Status      ProjectStatus `json:"status"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	CreatedBy   *int64        `json:"created_by"`
	UpdatedBy   *int64        `json:"updated_by"`
	RowVersion  int64         `json:"row_version"`
}

// Validate checks project invariants.
func (p Project) Validate() error {
	if p.EntityID <= 0 {
		return errors.New("services: entity_id required")
	}
	if strings.TrimSpace(p.Ref) == "" {
		return errors.New("services: ref required")
	}
	if strings.TrimSpace(p.Label) == "" {
		return errors.New("services: label required")
	}
	return nil
}

// CanTransition reports whether a project status change is legal.
func (p Project) CanTransition(to ProjectStatus) bool {
	switch p.Status {
	case ProjectDraft:
		return to == ProjectActive || to == ProjectCanceled
	case ProjectActive:
		return to == ProjectOnHold || to == ProjectClosed || to == ProjectCanceled
	case ProjectOnHold:
		return to == ProjectActive || to == ProjectClosed || to == ProjectCanceled
	default:
		return false
	}
}

// Task is a unit of project work.
type Task struct {
	ID          int64      `json:"id"`
	EntityID    int64      `json:"entity_id"`
	ProjectID   int64      `json:"project_id"`
	Label       string     `json:"label"`
	Description string     `json:"description"`
	Status      TaskStatus `json:"status"`
	Assignee    string     `json:"assignee"` // login, informational only
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	RowVersion  int64      `json:"row_version"`
}

// Validate checks task invariants (project liveness is enforced by the store).
func (t Task) Validate() error {
	if t.EntityID <= 0 || t.ProjectID <= 0 {
		return errors.New("services: entity_id and project_id required")
	}
	if strings.TrimSpace(t.Label) == "" {
		return errors.New("services: label required")
	}
	return nil
}

// CanTransition reports whether a task status change is legal.
func (t Task) CanTransition(to TaskStatus) bool {
	switch t.Status {
	case TaskTodo:
		return to == TaskDoing || to == TaskDone || to == TaskCanceled
	case TaskDoing:
		return to == TaskDone || to == TaskCanceled
	default:
		return false
	}
}

// TimeEntry books hours (hundredths) against a task (Dolibarr llx_projet_task_time).
type TimeEntry struct {
	ID        int64     `json:"id"`
	EntityID  int64     `json:"entity_id"`
	ProjectID int64     `json:"project_id"`
	TaskID    int64     `json:"task_id"`
	Author    string    `json:"author"` // login
	Hours     int64     `json:"hours"`  // hundredths of an hour, must be > 0
	EntryDate time.Time `json:"entry_date"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"created_at"`
}

// Validate checks time entry invariants.
func (e TimeEntry) Validate() error {
	if e.EntityID <= 0 || e.ProjectID <= 0 || e.TaskID <= 0 {
		return errors.New("services: entity_id, project_id and task_id required")
	}
	if strings.TrimSpace(e.Author) == "" {
		return errors.New("services: author required")
	}
	if e.Hours <= 0 {
		return errors.New("services: hours must be positive")
	}
	if e.EntryDate.IsZero() {
		return errors.New("services: entry_date required")
	}
	return nil
}

// ServiceContract is a subscription/service agreement with an org.
type ServiceContract struct {
	ID        int64          `json:"id"`
	EntityID  int64          `json:"entity_id"`
	Ref       string         `json:"ref"` // unique per entity (Dolibarr llx_contrat.ref)
	OrgID     int64          `json:"org_id"`
	Label     string         `json:"label"`
	Status    ContractStatus `json:"status"`
	StartDate *time.Time     `json:"start_date"`
	EndDate   *time.Time     `json:"end_date"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	RowVersion int64         `json:"row_version"`
}

// Validate checks contract invariants.
func (c ServiceContract) Validate() error {
	if c.EntityID <= 0 {
		return errors.New("services: entity_id required")
	}
	if strings.TrimSpace(c.Ref) == "" {
		return errors.New("services: ref required")
	}
	if c.OrgID <= 0 {
		return errors.New("services: org_id required")
	}
	if strings.TrimSpace(c.Label) == "" {
		return errors.New("services: label required")
	}
	if c.StartDate != nil && c.EndDate != nil && c.EndDate.Before(*c.StartDate) {
		return errors.New("services: end_date before start_date")
	}
	return nil
}

// CanTransition reports whether a contract status change is legal.
func (c ServiceContract) CanTransition(to ContractStatus) bool {
	switch c.Status {
	case ContractDraft:
		return to == ContractActive || to == ContractCanceled
	case ContractActive:
		return to == ContractSuspended || to == ContractClosed || to == ContractCanceled
	case ContractSuspended:
		return to == ContractActive || to == ContractClosed || to == ContractCanceled
	default:
		return false
	}
}

// Intervention is a field service sheet, optionally tied to a project/contract.
type Intervention struct {
	ID          int64              `json:"id"`
	EntityID    int64              `json:"entity_id"`
	Ref         string             `json:"ref"`
	OrgID       int64              `json:"org_id"`
	ProjectID   *int64             `json:"project_id"`
	ContractID  *int64             `json:"contract_id"`
	Label       string             `json:"label"`
	Description string             `json:"description"`
	Status      InterventionStatus `json:"status"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
	RowVersion  int64              `json:"row_version"`
}

// Validate checks intervention invariants.
func (i Intervention) Validate() error {
	if i.EntityID <= 0 {
		return errors.New("services: entity_id required")
	}
	if strings.TrimSpace(i.Ref) == "" {
		return errors.New("services: ref required")
	}
	if i.OrgID <= 0 {
		return errors.New("services: org_id required")
	}
	if strings.TrimSpace(i.Label) == "" {
		return errors.New("services: label required")
	}
	return nil
}

// CanTransition reports whether an intervention status change is legal.
func (i Intervention) CanTransition(to InterventionStatus) bool {
	switch i.Status {
	case InterventionScheduled:
		return to == InterventionInProgress || to == InterventionCanceled
	case InterventionInProgress:
		return to == InterventionDone || to == InterventionCanceled
	default:
		return false
	}
}

// Ticket is a helpdesk ticket (Dolibarr llx_ticket).
type Ticket struct {
	ID        int64        `json:"id"`
	EntityID  int64        `json:"entity_id"`
	Ref       string       `json:"ref"` // unique per entity
	OrgID     *int64       `json:"org_id"`
	ProjectID *int64       `json:"project_id"`
	Subject   string       `json:"subject"`
	Priority  int16        `json:"priority"` // 1 low .. 4 urgent
	Status    TicketStatus `json:"status"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
	RowVersion int64       `json:"row_version"`
}

// Validate checks ticket invariants.
func (t Ticket) Validate() error {
	if t.EntityID <= 0 {
		return errors.New("services: entity_id required")
	}
	if strings.TrimSpace(t.Ref) == "" {
		return errors.New("services: ref required")
	}
	if strings.TrimSpace(t.Subject) == "" {
		return errors.New("services: subject required")
	}
	if t.Priority < 1 || t.Priority > 4 {
		return fmt.Errorf("services: priority %d out of range 1..4", t.Priority)
	}
	return nil
}

// CanTransition reports whether a ticket status change is legal.
func (t Ticket) CanTransition(to TicketStatus) bool {
	switch t.Status {
	case TicketOpen:
		return to == TicketPending || to == TicketResolved || to == TicketClosed
	case TicketPending:
		return to == TicketOpen || to == TicketResolved || to == TicketClosed
	case TicketResolved:
		return to == TicketClosed || to == TicketOpen
	case TicketClosed:
		return to == TicketOpen // reopen
	default:
		return false
	}
}

// TicketMessage is one helpdesk exchange line (Dolibarr llx_ticket_msg).
type TicketMessage struct {
	ID        int64     `json:"id"`
	EntityID  int64     `json:"entity_id"`
	TicketID  int64     `json:"ticket_id"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	Internal  bool      `json:"internal"`
	CreatedAt time.Time `json:"created_at"`
}

// Validate checks message invariants.
func (m TicketMessage) Validate() error {
	if m.EntityID <= 0 || m.TicketID <= 0 {
		return errors.New("services: entity_id and ticket_id required")
	}
	if strings.TrimSpace(m.Author) == "" {
		return errors.New("services: author required")
	}
	if strings.TrimSpace(m.Body) == "" {
		return errors.New("services: body required")
	}
	return nil
}
