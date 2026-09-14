package manufacturing

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Service owns the produce transaction boundary (Phase 0 task 4):
// component consumption, finished-good receipt and the MO status flip commit
// atomically, closing the concurrent-producer race the lite scope accepted.
// A nil Pool runs the flow directly on the given db (memory stores in
// handler tests).
type Service struct {
	Pool   *pgxpool.Pool
	Store  Store
	Ledger Ledger
	Bus    platform.Bus
}

// NewService wires a produce service (Pool may be nil in tests).
func NewService(pool *pgxpool.Pool, store Store, ledger Ledger, bus platform.Bus) *Service {
	return &Service{Pool: pool, Store: store, Ledger: ledger, Bus: bus}
}

// ProduceCmd produces one manufacturing order within the caller's tenant.
type ProduceCmd struct {
	EntityID int64
	MOID     int64
}

// Produce consumes components, receipts the finished good and flips the MO
// to produced in one transaction. Publishes
// forgeerp.manufacturing.mo.produced.v1 after commit.
func (s *Service) Produce(ctx context.Context, cmd ProduceCmd) (ManufacturingOrder, ProducePlan, error) {
	if s.Pool == nil {
		mo, plan, err := s.produceOn(ctx, nil, cmd)
		if err != nil {
			return ManufacturingOrder{}, ProducePlan{}, err
		}
		s.published(ctx, cmd.EntityID, mo.ID)
		return mo, plan, nil
	}
	var mo ManufacturingOrder
	var plan ProducePlan
	err := platform.TxEntity(ctx, s.Pool, cmd.EntityID, func(tx pgx.Tx) error {
		var err error
		mo, plan, err = s.produceOn(ctx, tx, cmd)
		return err
	})
	if err != nil {
		return ManufacturingOrder{}, ProducePlan{}, err
	}
	s.published(ctx, cmd.EntityID, mo.ID)
	return mo, plan, nil
}

func (s *Service) published(ctx context.Context, entityID, id int64) {
	if s.Bus == nil {
		return
	}
	_ = s.Bus.Publish(ctx, platform.Event{
		Subject: "forgeerp.manufacturing.mo.produced.v1", Entity: "mo", EntityID: entityID, ID: id})
}

func (s *Service) produceOn(ctx context.Context, db platform.DBTX, cmd ProduceCmd) (ManufacturingOrder, ProducePlan, error) {
	mo, err := s.Store.MOByID(ctx, db, cmd.EntityID, cmd.MOID)
	if err != nil {
		return ManufacturingOrder{}, ProducePlan{}, err
	}
	lines, err := s.Store.LinesOf(ctx, db, mo.BOMID)
	if err != nil {
		return ManufacturingOrder{}, ProducePlan{}, err
	}
	// Ledger postings receive the same db handle, so on PostgreSQL they
	// join this transaction instead of posting in their own.
	plan, err := PostProduce(ctx, db, mo, lines, s.Ledger)
	if err != nil {
		return ManufacturingOrder{}, ProducePlan{}, err
	}
	done, err := s.Store.MarkProduced(ctx, db, cmd.EntityID, mo.ID, mo.RowVersion)
	if err != nil {
		return ManufacturingOrder{}, ProducePlan{}, err
	}
	return done, plan, nil
}
