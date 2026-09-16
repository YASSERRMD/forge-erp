package hr

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// HRMService orchestrates hire/terminate transitions. Pooled runs go through
// platform.TxEntity so the status flip commits inside the tenant-pinned
// transaction; a nil Pool runs directly on the given db (memory stores).
type HRMService struct {
	Pool  *pgxpool.Pool
	Store Store
	Bus   platform.Bus
}

// NewHRMService wires the service (Pool may be nil in tests).
func NewHRMService(pool *pgxpool.Pool, store Store, bus platform.Bus) *HRMService {
	return &HRMService{Pool: pool, Store: store, Bus: bus}
}

// Hire creates an active employee record.
func (s *HRMService) Hire(ctx context.Context, db platform.DBTX, entityID int64, e *Employee) error {
	e.EntityID = entityID
	e.Status = EmployeeActive
	if s.Pool == nil {
		if err := s.Store.CreateEmployee(ctx, db, e); err != nil {
			return err
		}
		s.published(ctx, entityID, e.ID, "forgeerp.hr.employee.hired.v1")
		return nil
	}
	var out *Employee = e
	err := platform.TxEntity(ctx, s.Pool, entityID, func(tx pgx.Tx) error {
		return s.Store.CreateEmployee(ctx, tx, out)
	})
	if err != nil {
		return err
	}
	s.published(ctx, entityID, e.ID, "forgeerp.hr.employee.hired.v1")
	return nil
}

// Terminate moves an employee to terminated (active/suspended → terminated).
func (s *HRMService) Terminate(ctx context.Context, db platform.DBTX, entityID int64, id int64, rowVersion int64) (Employee, error) {
	if s.Pool == nil {
		out, err := s.Store.SetEmployeeStatus(ctx, db, entityID, id, EmployeeTerminated, rowVersion)
		if err != nil {
			return Employee{}, err
		}
		s.published(ctx, entityID, id, "forgeerp.hr.employee.terminated.v1")
		return out, nil
	}
	var out Employee
	err := platform.TxEntity(ctx, s.Pool, entityID, func(tx pgx.Tx) error {
		var err error
		out, err = s.Store.SetEmployeeStatus(ctx, tx, entityID, id, EmployeeTerminated, rowVersion)
		return err
	})
	if err != nil {
		return Employee{}, err
	}
	s.published(ctx, entityID, id, "forgeerp.hr.employee.terminated.v1")
	return out, nil
}

func (s *HRMService) published(ctx context.Context, entityID, id int64, subject string) {
	if s.Bus == nil {
		return
	}
	_ = s.Bus.Publish(ctx, platform.Event{Subject: subject, Entity: "employee", EntityID: entityID, ID: id})
}
