// Package events implements event organization (Dolibarr eventorganization):
// events with seat capacity and attendee registrations, plus recruitment
// (llx_recruitment): open positions with applications moving through a hiring
// pipeline.
package events

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// OrgEvent status.
type OrgEventStatus int16

const (
	OrgEventDraft     OrgEventStatus = 0
	OrgEventPublished OrgEventStatus = 1
	OrgEventClosed    OrgEventStatus = 2
	OrgEventCanceled  OrgEventStatus = -1
)

// Registration status.
type RegistrationStatus int16

const (
	RegRegistered RegistrationStatus = 0
	RegConfirmed  RegistrationStatus = 1
	RegAttended   RegistrationStatus = 2
	RegCanceled   RegistrationStatus = -1
)

// Position status (recruitment).
type PositionStatus int16

const (
	PositionDraft  PositionStatus = 0
	PositionOpen   PositionStatus = 1
	PositionClosed PositionStatus = 2
)

// Application status (hiring pipeline).
type ApplicationStatus int16

const (
	AppReceived  ApplicationStatus = 0
	AppScreening ApplicationStatus = 1
	AppInterview ApplicationStatus = 2
	AppOffer     ApplicationStatus = 3
	AppHired     ApplicationStatus = 4
	AppRejected  ApplicationStatus = -1
)

// OrgEvent is a conference/training event with seat capacity.
type OrgEvent struct {
	ID          int64          `json:"id"`
	EntityID    int64          `json:"entity_id"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Location    string         `json:"location"`
	StartsAt    time.Time      `json:"starts_at"`
	EndsAt      time.Time      `json:"ends_at"`
	Capacity    int64          `json:"capacity"`
	Price       int64          `json:"price"` // minor units, 0 = free
	Status      OrgEventStatus `json:"status"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	RowVersion  int64          `json:"row_version"`
}

// Validate checks event invariants.
func (e OrgEvent) Validate() error {
	if e.EntityID <= 0 {
		return errors.New("events: entity_id required")
	}
	if strings.TrimSpace(e.Title) == "" {
		return errors.New("events: title required")
	}
	if e.StartsAt.IsZero() || e.EndsAt.IsZero() || !e.EndsAt.After(e.StartsAt) {
		return errors.New("events: valid starts_at/ends_at required")
	}
	if e.Capacity < 1 {
		return errors.New("events: capacity must be at least 1")
	}
	if e.Price < 0 {
		return errors.New("events: negative price")
	}
	return nil
}

// CanTransition reports whether an event status change is legal.
func (e OrgEvent) CanTransition(to OrgEventStatus) bool {
	switch e.Status {
	case OrgEventDraft:
		return to == OrgEventPublished || to == OrgEventCanceled
	case OrgEventPublished:
		return to == OrgEventClosed || to == OrgEventCanceled
	default:
		return false
	}
}

// Registration is one attendee seat.
type Registration struct {
	ID        int64              `json:"id"`
	EntityID  int64              `json:"entity_id"`
	EventID   int64              `json:"event_id"`
	Name      string             `json:"name"`
	Email     string             `json:"email"`
	OrgID     *int64             `json:"org_id"`
	Status    RegistrationStatus `json:"status"`
	CreatedAt time.Time          `json:"created_at"`
}

// Validate checks registration invariants.
func (r Registration) Validate() error {
	if r.EntityID <= 0 || r.EventID <= 0 {
		return errors.New("events: entity_id and event_id required")
	}
	if strings.TrimSpace(r.Name) == "" {
		return errors.New("events: name required")
	}
	return nil
}

// CanTransition reports whether a registration status change is legal.
func (r Registration) CanTransition(to RegistrationStatus) bool {
	switch r.Status {
	case RegRegistered:
		return to == RegConfirmed || to == RegAttended || to == RegCanceled
	case RegConfirmed:
		return to == RegAttended || to == RegCanceled
	default:
		return false
	}
}

// Position is an open job posting.
type Position struct {
	ID          int64          `json:"id"`
	EntityID    int64          `json:"entity_id"`
	Code        string         `json:"code"` // unique per entity
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Status      PositionStatus `json:"status"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	RowVersion  int64          `json:"row_version"`
}

// Validate checks position invariants.
func (p Position) Validate() error {
	if p.EntityID <= 0 {
		return errors.New("events: entity_id required")
	}
	if strings.TrimSpace(p.Code) == "" || strings.TrimSpace(p.Title) == "" {
		return errors.New("events: code and title required")
	}
	return nil
}

// CanTransition reports whether a position status change is legal.
func (p Position) CanTransition(to PositionStatus) bool {
	switch p.Status {
	case PositionDraft:
		return to == PositionOpen
	case PositionOpen:
		return to == PositionClosed
	default:
		return false
	}
}

// Application is one candidate in the pipeline.
type Application struct {
	ID          int64             `json:"id"`
	EntityID    int64             `json:"entity_id"`
	PositionID  int64             `json:"position_id"`
	Name        string            `json:"name"`
	Email       string            `json:"email"`
	Status      ApplicationStatus `json:"status"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	RowVersion  int64             `json:"row_version"`
}

// Validate checks application invariants.
func (a Application) Validate() error {
	if a.EntityID <= 0 || a.PositionID <= 0 {
		return errors.New("events: entity_id and position_id required")
	}
	if strings.TrimSpace(a.Name) == "" {
		return errors.New("events: name required")
	}
	return nil
}

// CanTransition walks the hiring pipeline (rejection from any open stage).
func (a Application) CanTransition(to ApplicationStatus) bool {
	if to == AppRejected {
		return a.Status == AppReceived || a.Status == AppScreening ||
			a.Status == AppInterview || a.Status == AppOffer
	}
	switch a.Status {
	case AppReceived:
		return to == AppScreening
	case AppScreening:
		return to == AppInterview
	case AppInterview:
		return to == AppOffer
	case AppOffer:
		return to == AppHired
	default:
		return false
	}
}

// SeatsTaken counts live registrations.
func SeatsTaken(regs []Registration) int64 {
	var n int64
	for _, r := range regs {
		if r.Status != RegCanceled {
			n++
		}
	}
	return n
}

// CheckCapacity enforces event capacity.
func CheckCapacity(capacity int64, regs []Registration) error {
	if taken := SeatsTaken(regs); taken >= capacity {
		return fmt.Errorf("events: event full (%d seats)", capacity)
	}
	return nil
}
