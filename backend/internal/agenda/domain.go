// Package agenda implements calendar events with reminders (Dolibarr
// comm/action + notify_def agenda reminders): scheduled events linked to
// orgs/projects, attendee lists, and a due-reminder query the scheduler
// (or cron hitting the dispatch endpoint) drains exactly once.
package agenda

import (
	"errors"
	"strings"
	"time"
)

// Event status.
type EventStatus int16

const (
	EventScheduled EventStatus = 0
	EventDone      EventStatus = 1
	EventCanceled  EventStatus = -1
)

// Event is one calendar entry.
type Event struct {
	ID          int64       `json:"id"`
	EntityID    int64       `json:"entity_id"`
	Title       string      `json:"title"`
	Description string      `json:"description"`
	Location    string      `json:"location"`
	StartAt     time.Time   `json:"start_at"`
	EndAt       time.Time   `json:"end_at"`
	AllDay      bool        `json:"all_day"`
	OwnerLogin  string      `json:"owner_login"`
	Attendees   []string    `json:"attendees"`
	OrgID       *int64      `json:"org_id"`
	ProjectID   *int64      `json:"project_id"`
	ReminderMin int64       `json:"reminder_min"` // minutes before start, 0 = none
	RemindedAt  *time.Time  `json:"reminded_at"`
	Status      EventStatus `json:"status"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
	RowVersion  int64       `json:"row_version"`
}

// Validate checks event invariants.
func (e Event) Validate() error {
	if e.EntityID <= 0 {
		return errors.New("agenda: entity_id required")
	}
	if strings.TrimSpace(e.Title) == "" {
		return errors.New("agenda: title required")
	}
	if e.StartAt.IsZero() || e.EndAt.IsZero() {
		return errors.New("agenda: start_at and end_at required")
	}
	if !e.EndAt.After(e.StartAt) {
		return errors.New("agenda: end_at must be after start_at")
	}
	if strings.TrimSpace(e.OwnerLogin) == "" {
		return errors.New("agenda: owner_login required")
	}
	if e.ReminderMin < 0 {
		return errors.New("agenda: negative reminder")
	}
	return nil
}

// CanTransition reports whether an event status change is legal.
func (e Event) CanTransition(to EventStatus) bool {
	if e.Status == EventScheduled {
		return to == EventDone || to == EventCanceled
	}
	return false
}

// ReminderDue reports whether a reminder should fire now (scheduled future
// event, reminder set, window reached, not yet sent).
func (e Event) ReminderDue(now time.Time) bool {
	if e.Status != EventScheduled || e.ReminderMin <= 0 || e.RemindedAt != nil {
		return false
	}
	return !now.Before(e.StartAt.Add(-time.Duration(e.ReminderMin) * time.Minute))
}
