package datapolicy

import (
	"context"
	"fmt"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// SubjectsFunc projects closable records for a scope (orgs/members seams
// live with the caller — this package stores no subject rows).
type SubjectsFunc func(ctx context.Context, entityID int64, scope string) ([]Subject, error)

// Service owns the retention boundary: dry-run reports, erasure logging,
// and the anonymise plan (pure domain routines do the field work).
type Service struct {
	Store    Store
	Subjects SubjectsFunc
	DB       platform.DBTX
	Now      func() time.Time
	Bus      platform.Bus
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func (s *Service) published(ctx context.Context, entityID, id int64, subject, entity string) {
	if s.Bus == nil {
		return
	}
	_ = s.Bus.Publish(ctx, platform.Event{Subject: subject, Entity: entity, EntityID: entityID, ID: id})
}

// DryRunReport is the dry-run payload: rule + per-subject due analysis.
// Nothing is mutated — the endpoint is read-only by construction.
type DryRunReport struct {
	Scope      string      `json:"scope"`
	RetainDays int64       `json:"retain_days"`
	Candidates []Candidate `json:"candidates"`
	DueCount   int64       `json:"due_count"`
}

// DryRun builds the report for a scope (missing rule → ErrNotFound so the
// caller configures retention first).
func (s *Service) DryRun(ctx context.Context, entityID int64, scope string) (DryRunReport, error) {
	if scope != ScopeOrgs && scope != ScopeMembers {
		return DryRunReport{}, fmt.Errorf("datapolicy: unknown scope %q: %w", scope, platform.ErrValidation)
	}
	rule, err := s.Store.RuleByScope(ctx, s.DB, entityID, scope)
	if err != nil {
		return DryRunReport{}, err
	}
	var subjects []Subject
	if s.Subjects != nil {
		subjects, err = s.Subjects(ctx, entityID, scope)
		if err != nil {
			return DryRunReport{}, err
		}
	}
	cands := DueSubjects(rule.RetainDays, s.now(), subjects)
	var due int64
	for _, c := range cands {
		if c.Due {
			due++
		}
	}
	return DryRunReport{Scope: scope, RetainDays: rule.RetainDays, Candidates: cands, DueCount: due}, nil
}

// RequestErasure logs an erasure request (GDPR Art. 17 audit trail).
func (s *Service) RequestErasure(ctx context.Context, entityID int64, scope string, subjectID int64, reason string) (ErasureRequest, error) {
	e := &ErasureRequest{EntityID: entityID, Scope: scope, SubjectID: subjectID, Reason: reason, Status: ErasurePending}
	if err := s.Store.LogErasure(ctx, s.DB, e); err != nil {
		return ErasureRequest{}, err
	}
	s.published(ctx, entityID, e.ID, "forgeerp.datapolicy.erasure.requested.v1", "erasure")
	return *e, nil
}

// CompleteErasure marks a pending request done (the anonymise routine
// itself runs against the owning package's rows via the caller's seam).
func (s *Service) CompleteErasure(ctx context.Context, entityID, id int64) (ErasureRequest, error) {
	e, err := s.Store.MarkErasureDone(ctx, s.DB, entityID, id)
	if err != nil {
		return ErasureRequest{}, err
	}
	s.published(ctx, entityID, e.ID, "forgeerp.datapolicy.erasure.completed.v1", "erasure")
	return e, nil
}
