// HRM employee records (Dolibarr llx_user + HR satellites, reshaped):
// ferp_employees are employment records DISTINCT from identity users.
// There is deliberately NO FK to identity users: logins are authentication
// handles (renamable, deactivatable, possibly several per human or shared
// across entities) while HR needs a stable per-entity record with its own
// code, establishment posting, job title, hire date and lifecycle status.
package hr

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Employee lifecycle status.
type EmployeeStatus int16

const (
	EmployeeActive     EmployeeStatus = 0
	EmployeeSuspended  EmployeeStatus = 1
	EmployeeTerminated EmployeeStatus = 2
)

// Establishment is a work site / office posting (Dolibarr establishment satellite).
type Establishment struct {
	ID         int64     `json:"id"`
	EntityID   int64     `json:"entity_id"`
	Code       string    `json:"code"` // unique per entity
	Label      string    `json:"label"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	RowVersion int64     `json:"row_version"`
}

// Validate checks establishment invariants.
func (e Establishment) Validate() error {
	if e.EntityID <= 0 {
		return errors.New("hr: entity_id required")
	}
	if strings.TrimSpace(e.Code) == "" {
		return errors.New("hr: establishment code required")
	}
	if strings.TrimSpace(e.Label) == "" {
		return errors.New("hr: establishment label required")
	}
	return nil
}

// Employee is one employment record scoped to an entity.
type Employee struct {
	ID              int64          `json:"id"`
	EntityID        int64          `json:"entity_id"`
	Code            string         `json:"code"` // unique per entity
	Name            string         `json:"name"`
	EstablishmentID int64          `json:"establishment_id"`
	JobTitle        string         `json:"job_title"`
	HireDate        time.Time      `json:"hire_date"`
	Status          EmployeeStatus `json:"status"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	RowVersion      int64          `json:"row_version"`
}

// Validate checks employee invariants.
func (e Employee) Validate() error {
	if e.EntityID <= 0 {
		return errors.New("hr: entity_id required")
	}
	if strings.TrimSpace(e.Code) == "" {
		return errors.New("hr: employee code required")
	}
	if strings.TrimSpace(e.Name) == "" {
		return errors.New("hr: employee name required")
	}
	if e.EstablishmentID <= 0 {
		return errors.New("hr: establishment_id required")
	}
	if strings.TrimSpace(e.JobTitle) == "" {
		return errors.New("hr: job_title required")
	}
	if e.HireDate.IsZero() {
		return errors.New("hr: hire_date required")
	}
	if e.HireDate.After(time.Now().Add(24 * time.Hour)) {
		return errors.New("hr: hire_date cannot be in the future")
	}
	return nil
}

// CanTransition reports whether an employee status change is legal.
// Active↔suspended, active/suspended→terminated; terminated is terminal.
func (e Employee) CanTransition(to EmployeeStatus) bool {
	switch e.Status {
	case EmployeeActive:
		return to == EmployeeSuspended || to == EmployeeTerminated
	case EmployeeSuspended:
		return to == EmployeeActive || to == EmployeeTerminated
	default:
		return false
	}
}

// EmployeeSkill is one declared skill on an employee (level 1..5).
type EmployeeSkill struct {
	ID         int64     `json:"id"`
	EntityID   int64     `json:"entity_id"`
	EmployeeID int64     `json:"employee_id"`
	Skill      string    `json:"skill"`
	Level      int16     `json:"level"` // 1..5
	CreatedAt  time.Time `json:"created_at"`
}

// Validate checks skill invariants.
func (s EmployeeSkill) Validate() error {
	if s.EntityID <= 0 || s.EmployeeID <= 0 {
		return errors.New("hr: entity_id and employee_id required")
	}
	if strings.TrimSpace(s.Skill) == "" {
		return errors.New("hr: skill required")
	}
	if s.Level < 1 || s.Level > 5 {
		return fmt.Errorf("hr: skill level %d out of range 1..5", s.Level)
	}
	return nil
}

// Evaluation is one periodic review (rating 1..5).
type Evaluation struct {
	ID         int64     `json:"id"`
	EntityID   int64     `json:"entity_id"`
	EmployeeID int64     `json:"employee_id"`
	Period     string    `json:"period"` // YYYY-MM
	Rating     int16     `json:"rating"` // 1..5
	Notes      string    `json:"notes"`
	CreatedAt  time.Time `json:"created_at"`
}

// Validate checks evaluation invariants.
func (e Evaluation) Validate() error {
	if e.EntityID <= 0 || e.EmployeeID <= 0 {
		return errors.New("hr: entity_id and employee_id required")
	}
	if !periodPattern.MatchString(e.Period) {
		return fmt.Errorf("hr: bad period %q, want YYYY-MM", e.Period)
	}
	if e.Rating < 1 || e.Rating > 5 {
		return fmt.Errorf("hr: rating %d out of range 1..5", e.Rating)
	}
	return nil
}
