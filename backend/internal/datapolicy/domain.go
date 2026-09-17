// Package datapolicy implements GDPR retention + anonymisation: retention
// rules per scope (orgs/members: anonymize after N days post-closure), an
// anonymise routine replacing PII fields with redacted markers while keeping
// ledger amounts, an erasure request log, and a dry-run report endpoint.
package datapolicy

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Scopes.
const (
	ScopeOrgs    = "orgs"
	ScopeMembers = "members"
)

// Actions.
const (
	ActionAnonymize = "anonymize"
)

// Erasure statuses.
const (
	ErasurePending = "pending"
	ErasureDone    = "done"
)

// RedactedDomain is the placeholder domain for anonymised emails
// (RFC 2606 .invalid — never routable, never confused with real data).
const RedactedDomain = "example.invalid"

// RetentionRule keeps rows of one scope for RetainDays after closure, then
// the Action applies (currently only "anonymize").
type RetentionRule struct {
	ID         int64  `json:"id"`
	EntityID   int64  `json:"entity_id"`
	Scope      string `json:"scope"`
	RetainDays int64  `json:"retain_days"`
	Action     string `json:"action"`
}

// Validate checks rule invariants.
func (r RetentionRule) Validate() error {
	if r.EntityID <= 0 {
		return errors.New("datapolicy: entity_id required")
	}
	if r.Scope != ScopeOrgs && r.Scope != ScopeMembers {
		return fmt.Errorf("datapolicy: unknown scope %q", r.Scope)
	}
	if r.RetainDays < 0 {
		return errors.New("datapolicy: retain_days negative")
	}
	if r.Action != ActionAnonymize {
		return fmt.Errorf("datapolicy: unknown action %q", r.Action)
	}
	return nil
}

// ErasureRequest logs one right-to-erasure request (GDPR Art. 17 audit trail).
type ErasureRequest struct {
	ID        int64     `json:"id"`
	EntityID  int64     `json:"entity_id"`
	Scope     string    `json:"scope"`
	SubjectID int64     `json:"subject_id"`
	Reason    string    `json:"reason"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// Validate checks erasure invariants.
func (e ErasureRequest) Validate() error {
	if e.EntityID <= 0 || e.SubjectID <= 0 {
		return errors.New("datapolicy: entity_id and subject_id required")
	}
	if e.Scope != ScopeOrgs && e.Scope != ScopeMembers {
		return fmt.Errorf("datapolicy: unknown scope %q", e.Scope)
	}
	if e.Status != "" && e.Status != ErasurePending && e.Status != ErasureDone {
		return fmt.Errorf("datapolicy: unknown status %q", e.Status)
	}
	return nil
}

// Subject is a closable record projected by the caller seam (orgs/members
// packages untouched): Open rows are never due; closed rows become due
// RetainDays after ClosedAt.
type Subject struct {
	ID       int64
	ClosedAt time.Time
	Open     bool
}

// Candidate is one dry-run row.
type Candidate struct {
	SubjectID int64     `json:"subject_id"`
	ClosedAt  time.Time `json:"closed_at"`
	DueAt     time.Time `json:"due_at"`
	Due       bool      `json:"due"`
}

// DueSubjects computes the dry-run report: every closed subject with its
// due instant; Due when now is at/after DueAt. Open subjects are excluded.
func DueSubjects(retainDays int64, now time.Time, subjects []Subject) []Candidate {
	out := []Candidate{}
	for _, s := range subjects {
		if s.Open {
			continue
		}
		dueAt := s.ClosedAt.AddDate(0, 0, int(retainDays))
		out = append(out, Candidate{SubjectID: s.ID, ClosedAt: s.ClosedAt,
			DueAt: dueAt, Due: !now.Before(dueAt)})
	}
	return out
}

// OrgPII is the anonymisable projection of an organization: PII is
// replaced; financial/ledger position is preserved.
type OrgPII struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Email       string `json:"email"`
	Phone       string `json:"phone"`
	Address     string `json:"address"`
	LedgerTotal int64  `json:"ledger_total"`
}

// AnonymizeOrg replaces PII with redacted markers, keeping LedgerTotal.
func AnonymizeOrg(o OrgPII) OrgPII {
	return OrgPII{
		ID:          o.ID,
		Name:        fmt.Sprintf("REDACTED-ORG-%d", o.ID),
		Email:       fmt.Sprintf("redacted+%d@%s", o.ID, RedactedDomain),
		Phone:       "",
		Address:     "REDACTED",
		LedgerTotal: o.LedgerTotal,
	}
}

// MemberPII is the anonymisable projection of a member.
type MemberPII struct {
	ID          int64  `json:"id"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	Company     string `json:"company"`
	Email       string `json:"email"`
	Phone       string `json:"phone"`
	LedgerTotal int64  `json:"ledger_total"`
}

// AnonymizeMember replaces PII with redacted markers, keeping LedgerTotal.
func AnonymizeMember(m MemberPII) MemberPII {
	return MemberPII{
		ID:          m.ID,
		FirstName:   "REDACTED",
		LastName:    fmt.Sprintf("REDACTED-%d", m.ID),
		Company:     "",
		Email:       fmt.Sprintf("redacted+%d@%s", m.ID, RedactedDomain),
		Phone:       "",
		LedgerTotal: m.LedgerTotal,
	}
}

// Redacted reports whether a string still looks like live PII (used by
// tests to assert field coverage): empty, REDACTED markers, and the
// .invalid domain pass; anything else fails.
func Redacted(s string) bool {
	if s == "" {
		return true
	}
	if strings.HasPrefix(s, "REDACTED") {
		return true
	}
	if strings.HasSuffix(s, "@"+RedactedDomain) {
		return true
	}
	return false
}
