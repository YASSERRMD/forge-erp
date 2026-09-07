// Package booking implements resource booking (Dolibarr bookcal/resource):
// bookable resources with capacity, and overlap-guarded reservations with a
// small lifecycle (booked → checked-in → completed, cancellable).
package booking

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Resource status.
type ResourceStatus int16

const (
	ResourceActive   ResourceStatus = 1
	ResourceInactive ResourceStatus = 0
)

// Booking status.
type BookingStatus int16

const (
	BookingBooked    BookingStatus = 0
	BookingCheckedIn BookingStatus = 1
	BookingCompleted BookingStatus = 2
	BookingCanceled  BookingStatus = -1
)

// Resource is something bookable (room, machine, vehicle).
type Resource struct {
	ID        int64          `json:"id"`
	EntityID  int64          `json:"entity_id"`
	Code      string         `json:"code"` // unique per entity
	Label     string         `json:"label"`
	Capacity  int64          `json:"capacity"` // concurrent seats, >= 1
	Status    ResourceStatus `json:"status"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	RowVersion int64         `json:"row_version"`
}

// Validate checks resource invariants.
func (r Resource) Validate() error {
	if r.EntityID <= 0 {
		return errors.New("booking: entity_id required")
	}
	if strings.TrimSpace(r.Code) == "" {
		return errors.New("booking: code required")
	}
	if strings.TrimSpace(r.Label) == "" {
		return errors.New("booking: label required")
	}
	if r.Capacity < 1 {
		return errors.New("booking: capacity must be at least 1")
	}
	return nil
}

// Booking is one reservation window [start, end).
type Booking struct {
	ID         int64         `json:"id"`
	EntityID   int64         `json:"entity_id"`
	ResourceID int64         `json:"resource_id"`
	OrgID      *int64        `json:"org_id"`
	UserLogin  string        `json:"user_login"`
	StartAt    time.Time     `json:"start_at"`
	EndAt      time.Time     `json:"end_at"`
	Seats      int64         `json:"seats"`
	Status     BookingStatus `json:"status"`
	CreatedAt  time.Time     `json:"created_at"`
	UpdatedAt  time.Time     `json:"updated_at"`
	RowVersion int64         `json:"row_version"`
}

// Validate checks booking invariants (overlap enforced by the store).
func (b Booking) Validate() error {
	if b.EntityID <= 0 || b.ResourceID <= 0 {
		return errors.New("booking: entity_id and resource_id required")
	}
	if strings.TrimSpace(b.UserLogin) == "" {
		return errors.New("booking: user_login required")
	}
	if b.StartAt.IsZero() || b.EndAt.IsZero() {
		return errors.New("booking: start_at and end_at required")
	}
	if !b.EndAt.After(b.StartAt) {
		return errors.New("booking: end_at must be after start_at")
	}
	if b.Seats < 1 {
		return errors.New("booking: seats must be at least 1")
	}
	return nil
}

// CanTransition reports whether a booking status change is legal.
func (b Booking) CanTransition(to BookingStatus) bool {
	switch b.Status {
	case BookingBooked:
		return to == BookingCheckedIn || to == BookingCompleted || to == BookingCanceled
	case BookingCheckedIn:
		return to == BookingCompleted || to == BookingCanceled
	default:
		return false
	}
}

// Overlaps reports whether two windows intersect (half-open [start, end)).
// Only live bookings (booked/checked-in) block capacity.
func Overlaps(aStart, aEnd, bStart, bEnd time.Time) bool {
	return aStart.Before(bEnd) && bStart.Before(aEnd)
}

// Live reports whether a booking occupies capacity.
func (b Booking) Live() bool {
	return b.Status == BookingBooked || b.Status == BookingCheckedIn
}

// FitsCapacity checks seats against overlapping live bookings.
func FitsCapacity(capacity int64, overlapping []Booking, want Booking) error {
	var used int64
	for _, o := range overlapping {
		if o.ID != want.ID && o.Live() && Overlaps(o.StartAt, o.EndAt, want.StartAt, want.EndAt) {
			used += o.Seats
		}
	}
	if used+want.Seats > capacity {
		return fmt.Errorf("booking: capacity exceeded (%d used, %d wanted, %d max)", used, want.Seats, capacity)
	}
	return nil
}
