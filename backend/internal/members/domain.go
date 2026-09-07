// Package members implements association membership (Dolibarr adherents):
// member types with annual fees, members with a small lifecycle, yearly
// subscriptions, and donations (llx_don) with promise→paid tracking.
package members

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Member status (Dolibarr llx_adherent.statut: 1 validated, 0 draft, -1 resigned).
type MemberStatus int16

const (
	MemberDraft    MemberStatus = 0
	MemberActive   MemberStatus = 1
	MemberResigned MemberStatus = -1
	MemberExcluded MemberStatus = -2
)

// Subscription status (Dolibarr cotisation flow).
type SubscriptionStatus int16

const (
	SubDraft     SubscriptionStatus = 0
	SubValidated SubscriptionStatus = 1
	SubPaid      SubscriptionStatus = 2
	SubCanceled  SubscriptionStatus = -1
)

// Donation status (Dolibarr llx_don.fk_statut).
type DonationStatus int16

const (
	DonationPromised DonationStatus = 0
	DonationPaid     DonationStatus = 1
	DonationCanceled DonationStatus = -1
)

var yearPattern = regexp.MustCompile(`^\d{4}$`)

// MemberType is a subscription class with its annual fee (llx_adherent_type).
type MemberType struct {
	ID        int64     `json:"id"`
	EntityID  int64     `json:"entity_id"`
	Code      string    `json:"code"` // unique per entity
	Label     string    `json:"label"`
	AnnualFee int64     `json:"annual_fee"` // minor units, >= 0
	CreatedAt time.Time `json:"created_at"`
}

// Validate checks member-type invariants.
func (t MemberType) Validate() error {
	if t.EntityID <= 0 {
		return errors.New("members: entity_id required")
	}
	if strings.TrimSpace(t.Code) == "" || strings.TrimSpace(t.Label) == "" {
		return errors.New("members: code and label required")
	}
	if t.AnnualFee < 0 {
		return errors.New("members: negative fee")
	}
	return nil
}

// Member is one adherent (person or company).
type Member struct {
	ID         int64        `json:"id"`
	EntityID   int64        `json:"entity_id"`
	Ref        string       `json:"ref"` // unique per entity
	TypeID     int64        `json:"type_id"`
	FirstName  string       `json:"first_name"`
	LastName   string       `json:"last_name"`
	Company    string       `json:"company"`
	Email      string       `json:"email"`
	Status     MemberStatus `json:"status"`
	CreatedAt  time.Time    `json:"created_at"`
	UpdatedAt  time.Time    `json:"updated_at"`
	RowVersion int64        `json:"row_version"`
}

// Validate checks member invariants (at least a person or company name).
func (m Member) Validate() error {
	if m.EntityID <= 0 {
		return errors.New("members: entity_id required")
	}
	if strings.TrimSpace(m.Ref) == "" {
		return errors.New("members: ref required")
	}
	if m.TypeID <= 0 {
		return errors.New("members: type_id required")
	}
	if strings.TrimSpace(m.FirstName) == "" && strings.TrimSpace(m.LastName) == "" && strings.TrimSpace(m.Company) == "" {
		return errors.New("members: person or company name required")
	}
	return nil
}

// CanTransition reports whether a member status change is legal.
func (m Member) CanTransition(to MemberStatus) bool {
	switch m.Status {
	case MemberDraft:
		return to == MemberActive
	case MemberActive:
		return to == MemberResigned || to == MemberExcluded
	default:
		return false
	}
}

// Subscription is one yearly fee payment.
type Subscription struct {
	ID        int64              `json:"id"`
	EntityID  int64              `json:"entity_id"`
	MemberID  int64              `json:"member_id"`
	Year      string             `json:"year"` // YYYY
	Amount    int64              `json:"amount"`
	Status    SubscriptionStatus `json:"status"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
	RowVersion int64             `json:"row_version"`
}

// Validate checks subscription invariants.
func (s Subscription) Validate() error {
	if s.EntityID <= 0 || s.MemberID <= 0 {
		return errors.New("members: entity_id and member_id required")
	}
	if !yearPattern.MatchString(s.Year) {
		return fmt.Errorf("members: bad year %q", s.Year)
	}
	if s.Amount <= 0 {
		return errors.New("members: amount must be positive")
	}
	return nil
}

// CanTransition reports whether a subscription status change is legal.
func (s Subscription) CanTransition(to SubscriptionStatus) bool {
	switch s.Status {
	case SubDraft:
		return to == SubValidated || to == SubCanceled
	case SubValidated:
		return to == SubPaid || to == SubCanceled
	default:
		return false
	}
}

// Donation is one gift (person/company donor, optional org link).
type Donation struct {
	ID        int64          `json:"id"`
	EntityID  int64          `json:"entity_id"`
	Ref       string         `json:"ref"` // unique per entity
	DonorName string         `json:"donor_name"`
	OrgID     *int64         `json:"org_id"`
	Amount    int64          `json:"amount"`
	DonatedAt time.Time      `json:"donated_at"`
	Method    string         `json:"method"` // cash|transfer|check|card
	Status    DonationStatus `json:"status"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	RowVersion int64         `json:"row_version"`
}

// Validate checks donation invariants.
func (d Donation) Validate() error {
	if d.EntityID <= 0 {
		return errors.New("members: entity_id required")
	}
	if strings.TrimSpace(d.Ref) == "" {
		return errors.New("members: ref required")
	}
	if strings.TrimSpace(d.DonorName) == "" {
		return errors.New("members: donor_name required")
	}
	if d.Amount <= 0 {
		return errors.New("members: amount must be positive")
	}
	if d.DonatedAt.IsZero() {
		return errors.New("members: donated_at required")
	}
	switch d.Method {
	case "cash", "transfer", "check", "card":
	default:
		return fmt.Errorf("members: unknown method %q", d.Method)
	}
	return nil
}

// CanTransition reports whether a donation status change is legal.
func (d Donation) CanTransition(to DonationStatus) bool {
	if d.Status == DonationPromised {
		return to == DonationPaid || to == DonationCanceled
	}
	return false
}
