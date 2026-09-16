package manufacturing

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
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
	if ops, err := s.Store.MOOperations(ctx, db, mo.ID); err != nil {
		return ManufacturingOrder{}, ProducePlan{}, err
	} else if len(ops) > 0 {
		// Scheduled MOs produce through operation completion: each step posts
		// its consume share and the final step posts the receipt. Direct
		// produce would double-post, so it is refused once a schedule exists.
		return ManufacturingOrder{}, ProducePlan{}, fmt.Errorf(
			"manufacturing: MO has scheduled operations, complete them instead: %w", platform.ErrValidation)
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

// ScheduleCmd forward-schedules an MO's routing x order qty from Start
// (zero Start means today UTC), replacing any pending schedule.
type ScheduleCmd struct {
	EntityID int64
	MOID     int64
	Start    time.Time
}

// Schedule builds the MO operation schedule and persists it atomically,
// publishing forgeerp.manufacturing.mo.scheduled.v1 after commit.
func (s *Service) Schedule(ctx context.Context, cmd ScheduleCmd) ([]MOOperation, error) {
	if s.Pool == nil {
		ops, err := s.scheduleOn(ctx, nil, cmd)
		if err != nil {
			return nil, err
		}
		s.publishEvent(ctx, cmd.EntityID, "forgeerp.manufacturing.mo.scheduled.v1", "mo", cmd.MOID)
		return ops, nil
	}
	var ops []MOOperation
	err := platform.TxEntity(ctx, s.Pool, cmd.EntityID, func(tx pgx.Tx) error {
		var err error
		ops, err = s.scheduleOn(ctx, tx, cmd)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.publishEvent(ctx, cmd.EntityID, "forgeerp.manufacturing.mo.scheduled.v1", "mo", cmd.MOID)
	return ops, nil
}

func (s *Service) publishEvent(ctx context.Context, entityID int64, subject, entity string, id int64) {
	if s.Bus == nil {
		return
	}
	_ = s.Bus.Publish(ctx, platform.Event{Subject: subject, Entity: entity, EntityID: entityID, ID: id})
}

func (s *Service) scheduleOn(ctx context.Context, db platform.DBTX, cmd ScheduleCmd) ([]MOOperation, error) {
	mo, err := s.Store.MOByID(ctx, db, cmd.EntityID, cmd.MOID)
	if err != nil {
		return nil, err
	}
	if mo.Status == MOProduced || mo.Status == MOCanceled {
		return nil, fmt.Errorf("manufacturing: cannot schedule a closed MO: %w", platform.ErrValidation)
	}
	routing, err := s.Store.BOMOperations(ctx, db, mo.BOMID)
	if err != nil {
		return nil, err
	}
	if len(routing) == 0 {
		return nil, fmt.Errorf("manufacturing: BOM has no routing operations: %w", platform.ErrValidation)
	}
	if existing, err := s.Store.MOOperations(ctx, db, mo.ID); err != nil {
		return nil, err
	} else {
		for _, o := range existing {
			if o.Status != MOOpPending {
				return nil, fmt.Errorf("manufacturing: operations already started, cannot reschedule: %w", platform.ErrValidation)
			}
		}
	}
	capacities := map[int64]int64{}
	for _, r := range routing {
		if _, ok := capacities[r.WorkstationID]; ok {
			continue
		}
		ws, err := s.Store.WorkstationByID(ctx, db, cmd.EntityID, r.WorkstationID)
		if err != nil {
			return nil, err
		}
		if ws.Status != WorkstationActive {
			return nil, fmt.Errorf("manufacturing: workstation %q is inactive: %w", ws.Code, platform.ErrValidation)
		}
		capacities[r.WorkstationID] = ws.DailyCapacityMin
	}
	start := cmd.Start
	if start.IsZero() {
		start = time.Now().UTC()
	}
	// Prior load across every workstation in the routing, so the new schedule
	// sees bookings from other MOs (overload flags instead of resolving).
	prior := DayLoad{}
	for wsID := range capacities {
		ops, err := s.Store.OperationsByWorkstation(ctx, db, cmd.EntityID, wsID,
			midnightUTC(start), midnightUTC(start).AddDate(0, 0, 365))
		if err != nil {
			return nil, err
		}
		for _, op := range ops {
			if op.MOID == mo.ID || op.Status == MOOpCanceled {
				continue
			}
			for d, v := range spreadOp(op, midnightUTC(start), midnightUTC(start).AddDate(0, 0, 365)) {
				m := prior[wsID]
				if m == nil {
					m = map[string]int64{}
					prior[wsID] = m
				}
				m[d] += v
			}
		}
	}
	ops, err := ScheduleOperations(routing, mo.Qty, start, capacities, prior)
	if err != nil {
		return nil, err
	}
	if err := s.Store.ReplaceMOOperations(ctx, db, cmd.EntityID, mo.ID, ops); err != nil {
		return nil, err
	}
	return s.Store.MOOperations(ctx, db, mo.ID)
}

// CompleteOperationCmd completes one scheduled operation: it records the
// actual minutes, posts that step's consume share through the catalog ledger,
// and — on the final step — posts the remaining consumes plus the
// finished-good receipt and flips the MO to produced (the existing produce
// path, same transaction).
type CompleteOperationCmd struct {
	EntityID      int64
	MOID          int64
	Seq           int32
	ActualMinutes int64
}

// CompleteOperationResult carries the completed step and, on the final step,
// the produced MO.
type CompleteOperationResult struct {
	Operation MOOperation        `json:"operation"`
	MO        ManufacturingOrder `json:"mo"`
	Produced  bool               `json:"produced"`
}

// CompleteOperation runs the completion flow, publishing
// forgeerp.manufacturing.mo.operation.completed.v1 (and the produced event on
// the final step) after commit.
func (s *Service) CompleteOperation(ctx context.Context, cmd CompleteOperationCmd) (CompleteOperationResult, error) {
	if s.Pool == nil {
		res, err := s.completeOn(ctx, nil, cmd)
		if err != nil {
			return CompleteOperationResult{}, err
		}
		s.publishEvent(ctx, cmd.EntityID, "forgeerp.manufacturing.mo.operation.completed.v1", "mo_operation", res.Operation.ID)
		if res.Produced {
			s.published(ctx, cmd.EntityID, res.MO.ID)
		}
		return res, nil
	}
	var res CompleteOperationResult
	err := platform.TxEntity(ctx, s.Pool, cmd.EntityID, func(tx pgx.Tx) error {
		var err error
		res, err = s.completeOn(ctx, tx, cmd)
		return err
	})
	if err != nil {
		return CompleteOperationResult{}, err
	}
	s.publishEvent(ctx, cmd.EntityID, "forgeerp.manufacturing.mo.operation.completed.v1", "mo_operation", res.Operation.ID)
	if res.Produced {
		s.published(ctx, cmd.EntityID, res.MO.ID)
	}
	return res, nil
}

func (s *Service) completeOn(ctx context.Context, db platform.DBTX, cmd CompleteOperationCmd) (CompleteOperationResult, error) {
	mo, err := s.Store.MOByID(ctx, db, cmd.EntityID, cmd.MOID)
	if err != nil {
		return CompleteOperationResult{}, err
	}
	if mo.Status != MOInProgress {
		return CompleteOperationResult{}, fmt.Errorf("manufacturing: MO must be in progress to complete operations: %w", platform.ErrValidation)
	}
	ops, err := s.Store.MOOperations(ctx, db, mo.ID)
	if err != nil {
		return CompleteOperationResult{}, err
	}
	if len(ops) == 0 {
		return CompleteOperationResult{}, fmt.Errorf("manufacturing: MO has no scheduled operations: %w", platform.ErrValidation)
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].Seq < ops[j].Seq })
	idx := -1
	for i, o := range ops {
		if o.Seq == cmd.Seq {
			idx = i
			break
		}
	}
	if idx < 0 {
		return CompleteOperationResult{}, fmt.Errorf("manufacturing: operation seq %d not found: %w", cmd.Seq, platform.ErrNotFound)
	}
	if ops[idx].Status != MOOpPending {
		return CompleteOperationResult{}, fmt.Errorf("manufacturing: operation seq %d already completed: %w", cmd.Seq, platform.ErrValidation)
	}
	for _, o := range ops[:idx] {
		if o.Status != MOOpDone {
			return CompleteOperationResult{}, fmt.Errorf("manufacturing: complete operation seq %d first: %w", o.Seq, platform.ErrValidation)
		}
	}
	lines, err := s.Store.LinesOf(ctx, db, mo.BOMID)
	if err != nil {
		return CompleteOperationResult{}, err
	}
	reqs, err := Explode(lines, mo.Qty)
	if err != nil {
		return CompleteOperationResult{}, err
	}
	n := int64(len(ops))
	final := idx == len(ops)-1
	// Sequential completion means exactly idx steps are done, each having
	// posted floor(total/n). The step posts floor share; the final step posts
	// the remainder (floor + total%n) so shares always sum to the total.
	share := func(total int64) int64 {
		if final {
			return total - (total/n)*int64(idx)
		}
		return total / n
	}
	for _, r := range reqs {
		need := share(r.Qty)
		if need == 0 {
			continue
		}
		lvl, err := s.Ledger.Level(ctx, db, r.ComponentID, mo.WarehouseID)
		if err != nil {
			return CompleteOperationResult{}, err
		}
		if lvl.Qty < need {
			return CompleteOperationResult{}, fmt.Errorf("manufacturing: insufficient component stock: %w", platform.ErrValidation)
		}
	}
	for _, r := range reqs {
		qty := share(r.Qty)
		if qty == 0 {
			continue
		}
		if _, err := s.Ledger.AppendMovement(ctx, db, &catalog.StockMovement{
			EntityID: mo.EntityID, ProductID: r.ComponentID, WarehouseID: mo.WarehouseID,
			Qty: -qty, Reason: catalog.ReasonConsume,
			Ref: fmt.Sprintf("%s:OP%d", mo.Ref, cmd.Seq)}, false); err != nil {
			return CompleteOperationResult{}, err
		}
	}
	done, err := s.Store.CompleteMOOperation(ctx, db, cmd.EntityID, mo.ID, cmd.Seq, cmd.ActualMinutes)
	if err != nil {
		return CompleteOperationResult{}, err
	}
	res := CompleteOperationResult{Operation: done, MO: mo}
	if !final {
		return res, nil
	}
	if _, err := s.Ledger.AppendMovement(ctx, db, &catalog.StockMovement{
		EntityID: mo.EntityID, ProductID: mo.ProductID, WarehouseID: mo.WarehouseID,
		Qty: mo.Qty, Reason: catalog.ReasonProduce, Ref: mo.Ref}, false); err != nil {
		return CompleteOperationResult{}, err
	}
	produced, err := s.Store.MarkProduced(ctx, db, cmd.EntityID, mo.ID, mo.RowVersion)
	if err != nil {
		return CompleteOperationResult{}, err
	}
	res.MO = produced
	res.Produced = true
	return res, nil
}
